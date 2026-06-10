package command

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/spf13/cobra"

	"github.com/austiecodes/rolio/internal/store"
)

func NewGlobCommand(rawAdapter store.Adapter, repo string) *cobra.Command {
	var allRepos bool
	var globLimit, globOffset int
	var longFmt bool

	cmd := &cobra.Command{
		Use:   "glob <pattern>",
		Short: "Find file paths by glob pattern",
		Long: `Discover file paths using glob patterns.

Supports:
  *     — match any non-/ characters
  ?     — match single non-/ character
  **    — match any path depth

Examples:
  rolio glob "**/*.md"              — all .md files in current repo
  rolio glob "docs/**/*.go"         — all .go files under docs/
  rolio glob "**/*.md" --all-repos  — all .md files across all repos`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateNonNeg([]string{"limit", "offset"}, globLimit, globOffset); err != nil {
				return err
			}
			pattern := args[0]

			if allRepos {
				return runGlobAllRepos(cmd, rawAdapter, pattern, globLimit, globOffset, longFmt)
			}

			return runGlobSingleRepo(cmd, rawAdapter, repo, pattern, globLimit, globOffset, longFmt)
		},
	}

	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "search across all repositories")
	cmd.Flags().IntVar(&globLimit, "limit", 0, "max results (0 = unlimited)")
	cmd.Flags().IntVar(&globOffset, "offset", 0, "skip first N results")
	cmd.Flags().BoolVar(&longFmt, "long", false, "show size and modification time")
	return cmd
}

func runGlobSingleRepo(cmd *cobra.Command, rawAdapter store.Adapter, repo, pattern string, limit, offset int, longFmt bool) error {
	resp, err := rawAdapter.Glob(context.Background(), store.GlobRequest{
		Repo:    repo,
		Pattern: pattern,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return err
	}
	for _, r := range resp.Results {
		if longFmt {
			fmt.Fprintf(cmd.OutOrStdout(), "/%s\t(%s)\t%s\n", r.Path, humanSize(r.Size), r.ModTime)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "/%s\n", r.Path)
		}
	}
	return nil
}

func runGlobAllRepos(cmd *cobra.Command, rawAdapter store.Adapter, pattern string, limit, offset int, longFmt bool) error {
	lister, ok := rawAdapter.(repoLister)
	if !ok {
		return fmt.Errorf("repo listing is not supported by the current adapter")
	}
	repos, err := lister.RepoList(context.Background())
	if err != nil {
		return err
	}

	// Fan-out: query each repo in parallel.
	g, gctx := errgroup.WithContext(context.Background())
	type repoResult struct {
		repo    string
		results []store.GlobResult
	}
	ch := make(chan repoResult, len(repos))
	for _, r := range repos {
		r := r
		g.Go(func() error {
			resp, err := rawAdapter.Glob(gctx, store.GlobRequest{
				Repo:    r,
				Pattern: pattern,
			})
			if err != nil {
				return err
			}
			ch <- repoResult{repo: r, results: resp.Results}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		close(ch)
		return err
	}
	close(ch)

	// Collect into composite slice and sort by repo, then path.
	type globEntry struct {
		repo   string
		result store.GlobResult
	}
	var entries []globEntry
	for res := range ch {
		for _, r := range res.results {
			entries = append(entries, globEntry{repo: res.repo, result: r})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].repo != entries[j].repo {
			return entries[i].repo < entries[j].repo
		}
		return entries[i].result.Path < entries[j].result.Path
	})

	// Apply offset/limit to merged results.
	if offset > 0 {
		if offset > len(entries) {
			offset = len(entries)
		}
		entries = entries[offset:]
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	for _, e := range entries {
		ref := "repo://" + url.PathEscape(e.repo) + "/" + e.result.Path
		if longFmt {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t(%s)\t%s\n", ref, humanSize(e.result.Size), e.result.ModTime)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", ref)
		}
	}
	return nil
}

func humanSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
	)
	switch {
	case size >= MB:
		return fmt.Sprintf("%.1fMB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.1fKB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%dB", size)
	}
}
