package command

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	mountadapter "github.com/austiecodes/gxfs/internal/mount"
	"github.com/austiecodes/gxfs/internal/store"
	"github.com/austiecodes/gxfs/internal/syncmanifest"
)

func NewHookCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "GXFS lifecycle hooks",
	}
	cmd.AddCommand(newHookSessionStartCommand(adapter, rawAdapter, repo, resolver))
	return cmd
}

func newHookSessionStartCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "Refresh gxfs docs for a new agent session",
		Long:  "Refresh manifest metadata and update materialized files that changed remotely. Runs with a 5s timeout; always exits 0 so it does not block the session.",
		RunE: func(cmd *cobra.Command, args []string) error {
			manifestPath := defaultManifestPath("")
			mountsPath := filepath.Join(filepath.Dir(manifestPath), "mounts.toml")

			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()

			// Run the hook; swallow all errors so we never block the session.
			updated, err := runHookSessionStart(ctx, adapter, rawAdapter, repo, resolver, manifestPath, mountsPath, cmd.OutOrStdout())
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "gxfs hook: %s\n", err)
				return nil
			}
			if updated > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "gxfs: updated %d file%s\n", updated, plural(updated))
			}
			return nil
		},
	}
}

// runHookSessionStart refreshes manifest metadata for all mounts and overwrites
// materialized files where the remote hash changed and the local file is unchanged.
// It never dematerializes entries.
func runHookSessionStart(ctx context.Context, adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver, manifestPath, mountsPath string, w io.Writer) (int, error) {
	// Load mounts to determine which roots to refresh.
	mountsCfg, err := loadMountsOrDefault(mountsPath)
	if err != nil {
		return 0, fmt.Errorf("load mounts: %w", err)
	}

	var roots []string
	for _, m := range mountsCfg.Mounts {
		root := cleanLocalDocsPath(m.Local)
		if root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return 0, nil
	}
	sort.Strings(roots)

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return 0, fmt.Errorf("load manifest: %w", err)
	}

	var totalUpdated int
	for _, root := range roots {
		// Collect current remote files.
		var remoteFiles []remoteSyncFile
		if resolver != nil {
			remoteFiles, err = collectMountedRemoteFiles(ctx, rawAdapter, resolver, repo, root, manifest)
		} else {
			remoteFiles, err = collectRemoteFiles(ctx, adapter, repo, root, manifest)
		}
		if err != nil {
			fmt.Fprintf(w, "gxfs: skip %s: %s\n", root, err)
			continue
		}

		// Build a lookup of remote files by local path.
		remoteByLocal := make(map[string]remoteSyncFile, len(remoteFiles))
		for _, rf := range remoteFiles {
			remoteByLocal[rf.LocalPath] = rf
		}

		// Iterate existing manifest entries under this root.
		// Use toManifestEntry to generate complete entries with all metadata.
		entries := syncmanifest.EntriesUnder(manifest, root)
		existingByLocal := manifestEntriesByLocal(syncmanifest.Manifest{Entries: entries})
		var updatedEntries []syncmanifest.Entry

		// Process existing entries.
		for i := range entries {
			entry := entries[i]
			rf, hasRemote := remoteByLocal[entry.Local]
			if !hasRemote {
				// File no longer exists remotely; keep entry as-is.
				updatedEntries = append(updatedEntries, entry)
				continue
			}

			// Generate a complete entry from the remote file.
			newEntry := rf.toManifestEntry(repo, root, entry.Materialized)

			if entry.Materialized && entry.ContentHash != rf.ContentHash {
				// Remote changed and file is materialized. Check if local is unchanged.
				local, localExists, localErr := readLocalSyncFile(entry.Local)
				if localErr != nil {
					fmt.Fprintf(w, "gxfs: skip %s: %s\n", entry.Local, localErr)
					// Keep old entry so manifest still matches local baseline.
					updatedEntries = append(updatedEntries, entry)
					continue
				}
				if localExists && local.ContentHash == entry.ContentHash {
					// Local unchanged, safe to overwrite with remote content.
					content := rf.Content
					if content == "" {
						catAdapter := adapter
						if rawAdapter != nil {
							catAdapter = rawAdapter
						}
						catRepo := rf.RemoteRepo
						if catRepo == "" {
							catRepo = repo
						}
						cat, catErr := catAdapter.Cat(ctx, store.CatRequest{Repo: catRepo, Path: rf.RemotePath})
						if catErr != nil {
							fmt.Fprintf(w, "gxfs: skip %s: %s\n", entry.Local, catErr)
							// Cat failed; keep old entry.
							updatedEntries = append(updatedEntries, entry)
							continue
						}
						content = cat.Content
					}
					if err := writeMaterializedFile(entry.Local, content); err != nil {
						fmt.Fprintf(w, "gxfs: skip %s: %s\n", entry.Local, err)
						// Write failed; keep old entry.
						updatedEntries = append(updatedEntries, entry)
						continue
					}
					// Successfully overwrote local file; use new entry.
					updatedEntries = append(updatedEntries, newEntry)
					totalUpdated++
					continue
				}
				// Local also changed (conflict). Keep old entry so manifest
				// still represents the local baseline, not the remote version.
				fmt.Fprintf(w, "gxfs: skip %s: local has unpushed changes\n", entry.Local)
				updatedEntries = append(updatedEntries, entry)
				continue
			}
			updatedEntries = append(updatedEntries, newEntry)
		}

		// Add new remote files not in manifest (as non-materialized).
		for _, rf := range remoteFiles {
			if _, exists := existingByLocal[rf.LocalPath]; !exists {
				updatedEntries = append(updatedEntries, rf.toManifestEntry(repo, root, false))
			}
		}

		manifest = syncmanifest.UpdateEntries(manifest, updatedEntries)
	}

	if err := syncmanifest.Save(manifestPath, manifest); err != nil {
		return totalUpdated, fmt.Errorf("save manifest: %w", err)
	}
	return totalUpdated, nil
}
