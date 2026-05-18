package command

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"gxfs/internal/client"
	mountadapter "gxfs/internal/mount"
	"gxfs/internal/store"
	"gxfs/internal/syncmanifest"
)

// isCrossRepoMount checks if a local path resolves to a cross-repo mount.
func isCrossRepoMount(localPath string, repo string, resolver *mountadapter.Resolver) bool {
	if resolver == nil {
		return false
	}
	resolved, err := resolver.Resolve(localPath, mountadapter.OpWrite)
	if err != nil {
		return false
	}
	return resolved.RemoteRepo != repo
}

// setMountPathOnClient sets the X-Mount-Path header on the raw client adapter
// if the path resolves to a cross-repo mount.
func setMountPathOnClient(rawAdapter store.Adapter, localPath string, resolver *mountadapter.Resolver) {
	if cl, ok := rawAdapter.(*client.Client); ok && resolver != nil {
		cl.SetMountPath(resolveMountPath(localPath, resolver))
	}
}

// manifestHashForPath returns the content_hash from the manifest for a given path.
// Returns (hash, true) if a manifest entry with a hash is found.
// Returns ("", false) if no entry or no hash.
func manifestHashForPath(localPath string) (string, bool) {
	manifestPath := defaultManifestPath("")
	manifest, err := syncmanifest.Load(manifestPath)
	if err != nil {
		return "", false
	}
	cleaned := strings.TrimPrefix(localPath, "/")
	for _, entry := range manifest.Entries {
		if entry.Local == cleaned || entry.Local == localPath {
			if entry.ContentHash != "" {
				return entry.ContentHash, true
			}
			return "", false
		}
	}
	return "", false
}

// resolveMountPath resolves the local mount prefix for a given path.
// Returns the mount local path (e.g. "libs/other") or "" if not on a mount.
func resolveMountPath(localPath string, resolver *mountadapter.Resolver) string {
	if resolver == nil {
		return ""
	}
	// Find the mount entry whose local prefix matches.
	for _, local := range resolver.MountLocals() {
		cleaned := strings.TrimPrefix(localPath, "/")
		if cleaned == local || strings.HasPrefix(cleaned, local+"/") {
			return local
		}
	}
	return ""
}

func NewWriteCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "write <path> [content]",
		Short: "Write content to a VFS file",
		Long:  "Write content to a VFS file, creating parent directories as needed.\nIf content is not provided as an argument, reads from stdin.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			var content string
			if len(args) == 2 {
				content = args[1]
			} else {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				content = string(data)
			}

			var expectedHash string
			crossRepo := isCrossRepoMount(path, repo, resolver)
			if crossRepo {
				hash, hasBaseline := manifestHashForPath(path)
				if hasBaseline {
					expectedHash = hash
				} else {
					// No baseline — stat remote to decide create vs error.
					_, statErr := adapter.Stat(cmd.Context(), store.StatRequest{Repo: repo, Path: path})
					if statErr == nil {
						return fmt.Errorf("path %s exists remotely but has no local baseline; run 'gxfs refresh' or 'gxfs sync pull' first", path)
					}
					if !errors.Is(statErr, store.ErrNotFound) {
						return fmt.Errorf("stat %s: %w", path, statErr)
					}
					// Remote does not exist — create-only.
					expectedHash = "*"
				}
			}
			setMountPathOnClient(rawAdapter, path, resolver)
			resp, err := adapter.Put(cmd.Context(), store.PutRequest{
				Repo:         repo,
				Path:         path,
				Content:      content,
				ExpectedHash: expectedHash,
			})
			if err != nil {
				if errors.Is(err, store.ErrConflict) {
					return fmt.Errorf("%w\nhint: run 'gxfs sync pull' or 'gxfs refresh' to update local state, then retry", err)
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes)\n", resp.Node.Path, resp.Node.Size)
			return nil
		},
	}
	return cmd
}

func NewDeleteCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <path>",
		Short: "Delete a VFS file or directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			var expectedHash string
			if isCrossRepoMount(path, repo, resolver) {
				hash, hasBaseline := manifestHashForPath(path)
				if !hasBaseline {
					return fmt.Errorf("path %s has no local baseline; run 'gxfs refresh' or 'gxfs sync pull' first", path)
				}
				expectedHash = hash
			}
			setMountPathOnClient(rawAdapter, path, resolver)
			_, err := adapter.Delete(cmd.Context(), store.DeleteRequest{
				Repo:         repo,
				Path:         path,
				ExpectedHash: expectedHash,
			})
			if err != nil {
				if errors.Is(err, store.ErrConflict) {
					return fmt.Errorf("%w\nhint: run 'gxfs sync pull' or 'gxfs refresh' to update local state, then retry", err)
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", path)
			return nil
		},
	}
	return cmd
}

func NewEditCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var old, newStr string
	var all bool

	cmd := &cobra.Command{
		Use:   "edit <path> --old <text> --new <text> [--all]",
		Short: "Replace text in a VFS file",
		Long:  "Replace occurrences of old text with new text in a VFS file.\nBy default replaces only the first occurrence. Use --all to replace all.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			var expectedHash string
			if isCrossRepoMount(path, repo, resolver) {
				hash, hasBaseline := manifestHashForPath(path)
				if !hasBaseline {
					return fmt.Errorf("path %s has no local baseline; run 'gxfs refresh' or 'gxfs sync pull' first", path)
				}
				expectedHash = hash
			}
			setMountPathOnClient(rawAdapter, path, resolver)
			resp, err := adapter.Edit(cmd.Context(), store.EditRequest{
				Repo:         repo,
				Path:         path,
				Old:          old,
				New:          newStr,
				All:          all,
				ExpectedHash: expectedHash,
			})
			if err != nil {
				if errors.Is(err, store.ErrConflict) {
					return fmt.Errorf("%w\nhint: run 'gxfs sync pull' or 'gxfs refresh' to update local state, then retry", err)
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "edited %s (%d replacement%s)\n", resp.Path, resp.Replaced, plural(resp.Replaced))
			return nil
		},
	}
	cmd.Flags().StringVar(&old, "old", "", "text to find (required)")
	cmd.Flags().StringVar(&newStr, "new", "", "replacement text (required)")
	cmd.Flags().BoolVar(&all, "all", false, "replace all occurrences")
	cmd.MarkFlagRequired("old")
	cmd.MarkFlagRequired("new")
	return cmd
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// printPaginationSummary prints a "showing X-Y of Z" line when results are
// paginated and there are more items beyond the current page.
func printPaginationSummary(out io.Writer, offset, shown, total int) {
	if total <= shown || shown == 0 {
		return
	}
	fmt.Fprintf(out, "\nshowing %d-%d of %d\n", offset+1, offset+shown, total)
}

// validateNonNeg returns an error if any of the named int values are negative.
func validateNonNeg(names []string, vals ...int) error {
	for i, v := range vals {
		if v < 0 {
			return fmt.Errorf("--%s must be non-negative", names[i])
		}
	}
	return nil
}
