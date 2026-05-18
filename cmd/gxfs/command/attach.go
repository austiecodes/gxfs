package command

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"gxfs/internal/config"
	"gxfs/internal/store"
)

func NewAttachCommand(rawAdapter store.Adapter, repo string) *cobra.Command {
	var dryRun bool
	var force bool

	cmd := &cobra.Command{
		Use:   "attach <keyword-or-repo> --into <local-path>",
		Short: "Auto-discover and mount a remote repository",
		Long: `Discover a repository by keyword and mount it.

Searches server repos for a match by exact name or suffix on the last
path segment. On unique match, automatically runs the equivalent of
mount add.

Examples:
  gxfs attach openai-go --into docs/lib/openai-go
  gxfs attach github/openai-go --into docs/lib/openai-go
  gxfs attach openai --into docs/lib/openai-go --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keyword := args[0]
			localPath := cleanMountLocal(cmd.Flag("into").Value.String())

			if localPath == "" {
				return fmt.Errorf("--into is required and must be a non-empty relative path")
			}

			lister, ok := rawAdapter.(repoLister)
			if !ok {
				return fmt.Errorf("repo listing is not supported by the current adapter")
			}
			repos, err := lister.RepoList(cmd.Context())
			if err != nil {
				return err
			}

			// Match repos: exact match first, then suffix match on last segment.
			var matches []string
			for _, r := range repos {
				if r == keyword {
					matches = []string{r}
					break
				}
				// Suffix match on last path segment
				parts := strings.Split(r, "/")
				lastSegment := parts[len(parts)-1]
				if lastSegment == keyword {
					matches = append(matches, r)
				}
			}

			switch len(matches) {
			case 0:
				return fmt.Errorf("no repos matched %q. Use 'gxfs repo list' to see available repos", keyword)
			case 1:
				// Unique match — proceed
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "Multiple repos matched %q:\n", keyword)
				for i, m := range matches {
					fmt.Fprintf(cmd.OutOrStdout(), "  %d. %s\n", i+1, m)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Use: gxfs attach <exact-repo-name> --into %s\n", localPath)
				return fmt.Errorf("ambiguous match: %d repos", len(matches))
			}

			targetRepo := matches[0]
			remoteRef := "repo://" + url.PathEscape(targetRepo) + "/"
			mode := "readonly"

			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] would mount: %s → %s (%s)\n", localPath, remoteRef, mode)
				return nil
			}

			// Validate target repo exists (root stat).
			if _, err := rawAdapter.Stat(cmd.Context(), store.StatRequest{Repo: targetRepo, Path: "/"}); err != nil {
				return fmt.Errorf("remote repo %s does not exist: %w", targetRepo, err)
			}

			// Save mount config.
			mountsPath := defaultMountsPath()
			mountsCfg, err := loadMountsOrDefault(mountsPath)
			if err != nil {
				return err
			}

			// Check existing mount at same local path.
			for i, m := range mountsCfg.Mounts {
				if m.Local == localPath {
					if !force {
						return fmt.Errorf("mount for %s already exists (use --force to replace)", localPath)
					}
					mountsCfg.Mounts[i] = config.MountConfig{
						Local:  localPath,
						Remote: remoteRef,
						Mode:   mode,
						Source: "attach",
					}
					if err := config.SaveMounts(mountsPath, mountsCfg); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "replaced mount %s → %s (%s)\n", localPath, remoteRef, mode)
					return refreshAfterMount(cmd, rawAdapter, repo, localPath, mountsPath, false)
				}
			}

			mountsCfg.Mounts = append(mountsCfg.Mounts, config.MountConfig{
				Local:  localPath,
				Remote: remoteRef,
				Mode:   mode,
				Source: "attach",
			})
			if err := config.SaveMounts(mountsPath, mountsCfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "attached %s → %s (%s)\n", localPath, remoteRef, mode)
			return refreshAfterMount(cmd, rawAdapter, repo, localPath, mountsPath, false)
		},
	}

	cmd.Flags().String("into", "", "local path to mount into (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be mounted without doing it")
	cmd.Flags().BoolVar(&force, "force", false, "replace existing mount at same local path")
	return cmd
}
