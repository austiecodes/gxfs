package command

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"gxfs/internal/config"
	mountadapter "gxfs/internal/mount"
	"gxfs/internal/store"
	"gxfs/internal/syncmanifest"
)

func NewMountCommand(adapter, rawAdapter store.Adapter, repo string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mount",
		Short: "Manage mount points",
	}
	cmd.AddCommand(newMountAddCommand(rawAdapter, repo))
	cmd.AddCommand(newMountRemoveCommand())
	cmd.AddCommand(newMountListCommand())
	return cmd
}

func newMountAddCommand(rawAdapter store.Adapter, repo string) *cobra.Command {
	var mode string
	var force bool
	var noRefresh bool

	cmd := &cobra.Command{
		Use:   "add <remote-ref> <local-path>",
		Short: "Add a mount point",
		Long:  "Add a mount point mapping a remote path to a local path.\nSupports repo://self/<path> and repo://<other-repo>/<path> references.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			remoteRef := args[0]
			localPath := cleanMountLocal(args[1])

			if localPath == "" {
				return fmt.Errorf("local path must be a non-empty relative path")
			}

			// Parse the remote ref to extract target repo and path.
			targetRepo, remotePath, err := mountadapter.ParseRemoteRef(repo, remoteRef)
			if err != nil {
				return err
			}

			if mode != "readonly" && mode != "writable" {
				return fmt.Errorf("mode must be readonly or writable, got %q", mode)
			}

			// Use raw adapter (direct client, no mount resolver) to validate
			// the remote path exists on the server, using the target repo.
			if remotePath == "/" {
				// Root mount: validate the target repo exists by statting its root.
				if _, err := rawAdapter.Stat(cmd.Context(), store.StatRequest{Repo: targetRepo, Path: "/"}); err != nil {
					return fmt.Errorf("remote repo %s does not exist: %w", targetRepo, err)
				}
			} else {
				if _, err := rawAdapter.Stat(cmd.Context(), store.StatRequest{Repo: targetRepo, Path: remotePath}); err != nil {
					return fmt.Errorf("remote path %s does not exist: %w", remoteRef, err)
				}
			}

			mountsPath := defaultMountsPath()
			mountsCfg, err := loadMountsOrDefault(mountsPath)
			if err != nil {
				return err
			}

			// Check conflicts
			for i, m := range mountsCfg.Mounts {
				if m.Local == localPath {
					if !force {
						return fmt.Errorf("mount for %s already exists (use --force to replace)", localPath)
					}
					// Replace existing
					mountsCfg.Mounts[i] = config.MountConfig{
						Local:  localPath,
						Remote: remoteRef,
						Mode:   mode,
						Source: "manual",
					}
					if err := config.SaveMounts(mountsPath, mountsCfg); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "replaced mount %s → %s (%s)\n", localPath, remoteRef, mode)
					return refreshAfterMount(cmd, rawAdapter, repo, localPath, mountsPath, noRefresh)
				}
			}

			// Check ancestor/descendant overlap
			for _, m := range mountsCfg.Mounts {
				if strings.HasPrefix(m.Local, localPath+"/") || strings.HasPrefix(localPath, m.Local+"/") {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s overlaps with existing mount %s\n", localPath, m.Local)
				}
			}

			mountsCfg.Mounts = append(mountsCfg.Mounts, config.MountConfig{
				Local:  localPath,
				Remote: remoteRef,
				Mode:   mode,
				Source: "manual",
			})
			if err := config.SaveMounts(mountsPath, mountsCfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added mount %s → %s (%s)\n", localPath, remoteRef, mode)

			return refreshAfterMount(cmd, rawAdapter, repo, localPath, mountsPath, noRefresh)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "readonly", "mount mode: readonly or writable")
	cmd.Flags().BoolVar(&force, "force", false, "replace existing mount at same local path")
	cmd.Flags().BoolVar(&noRefresh, "no-refresh", false, "skip manifest refresh after adding mount")
	return cmd
}

func newMountRemoveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <local-path>",
		Short: "Remove a mount point",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			localPath := cleanMountLocal(args[0])

			mountsPath := defaultMountsPath()
			mountsCfg, err := config.LoadMounts(mountsPath)
			if err != nil {
				return fmt.Errorf("load mounts: %w", err)
			}

			found := false
			var remaining []config.MountConfig
			for _, m := range mountsCfg.Mounts {
				if m.Local == localPath {
					found = true
					continue
				}
				remaining = append(remaining, m)
			}
			if !found {
				return fmt.Errorf("no mount found for %s", localPath)
			}

			// Check if materialized files exist under this mount
			manifestPath := defaultManifestPath("")
			if manifest, err := syncmanifest.Load(manifestPath); err == nil {
				entries := syncmanifest.EntriesUnder(manifest, localPath)
				for _, e := range entries {
					if e.Materialized {
						return fmt.Errorf("cannot remove mount %s: materialized files exist under this path (run `gxfs dematerialize %s` first)", localPath, localPath)
					}
				}
			}

			mountsCfg.Mounts = remaining
			if len(mountsCfg.Mounts) == 0 {
				mountsCfg.Mounts = []config.MountConfig{}
			}
			if err := config.SaveMounts(mountsPath, mountsCfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed mount %s\n", localPath)
			return nil
		},
	}
	return cmd
}

func newMountListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List current mount points",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mountsPath := defaultMountsPath()
			mountsCfg, err := loadMountsOrDefault(mountsPath)
			if err != nil {
				return fmt.Errorf("load mounts: %w", err)
			}
			if len(mountsCfg.Mounts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no mounts configured")
				return nil
			}
			for _, m := range mountsCfg.Mounts {
				source := ""
				if m.Source != "" && m.Source != "manual" {
					source = fmt.Sprintf("  [%s]", m.Source)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s%s\n", m.Local, m.Remote, m.Mode, source)
			}
			return nil
		},
	}
	return cmd
}

func cleanMountLocal(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return p
}

func defaultMountsPath() string {
	return filepath.Join(".gxfs", "mounts.toml")
}

func loadMountsOrDefault(mountsPath string) (config.MountsConfig, error) {
	mountsCfg, err := config.LoadMounts(mountsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config.MountsConfig{Version: 1, Mounts: []config.MountConfig{}}, nil
		}
		return config.MountsConfig{}, err
	}
	return mountsCfg, nil
}

func refreshAfterMount(cmd *cobra.Command, rawAdapter store.Adapter, repo, localPath, mountsPath string, noRefresh bool) error {
	if noRefresh {
		return nil
	}
	// Reload mounts config and build resolver for the newly added mount.
	mountsCfg, err := config.LoadMounts(mountsPath)
	if err != nil {
		return fmt.Errorf("reload mounts for refresh: %w", err)
	}
	resolver, err := mountadapter.NewResolver(repo, mountsCfg.Mounts)
	if err != nil {
		return fmt.Errorf("build resolver for refresh: %w", err)
	}

	manifestPath := defaultManifestPath("")
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("refresh manifest after mount: %w", err)
	}

	// Collect files using raw adapter + resolver so RemotePath contains
	// the true server path, not the localized display path.
	remoteFiles, err := collectMountedRemoteFiles(cmd.Context(), rawAdapter, resolver, repo, localPath, manifest)
	if err != nil {
		return fmt.Errorf("refresh manifest after mount: %w", err)
	}

	plan, err := buildRemoteSyncPlanFromFiles(repo, remoteFiles, manifest, remoteSyncOptions{}, localPath)
	if err != nil {
		return fmt.Errorf("refresh manifest after mount: %w", err)
	}

	// For mount add, we only accept remote files — no materialization or conflict resolution.
	// Use the mounted adapter for any write operations (pushLocalConflict).
	mountedAdapter := mountadapter.NewAdapter(rawAdapter, resolver)
	if _, err := applyRemoteSyncPlan(cmd.Context(), mountedAdapter, rawAdapter, repo, localPath, manifestPath, manifest, plan, resolver); err != nil {
		return fmt.Errorf("refresh manifest after mount: %w", err)
	}
	return nil
}
