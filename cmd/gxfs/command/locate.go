package command

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"gxfs/internal/store"
)

func NewLocateCommand(rawAdapter store.Adapter, repo string) *cobra.Command {
	var limit int
	var allRepos bool
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "locate <query>",
		Short: "Locate documents by lexical search",
		Long: `Locate documents by lexical search with ranking.

By default, searches the current repo. Use --all-repos to search across all
available repos. Results are returned with repo:// refs for easy follow-up
with 'gxfs cat' or 'gxfs attach'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateNonNeg([]string{"limit"}, limit); err != nil {
				return err
			}
			query := args[0]

			if allRepos {
				return locateAllRepos(cmd, rawAdapter, query, limit, jsonOutput)
			}

			locator, ok := rawAdapter.(store.Locator)
			if !ok {
				return fmt.Errorf("locate is not supported by this backend")
			}
			resp, err := locator.Locate(cmd.Context(), store.LocateRequest{
				Repo:  repo,
				Query: query,
				Limit: limit,
			})
			if err != nil {
				return err
			}
			if jsonOutput {
				return printLocateJSON(cmd, resp)
			}
			return printLocateHuman(cmd, resp)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "max results")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "search across all repos (fan-out)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func locateAllRepos(cmd *cobra.Command, rawAdapter store.Adapter, query string, limit int, jsonOutput bool) error {
	lister, ok := rawAdapter.(repoLister)
	if !ok {
		return fmt.Errorf("repo listing is not supported by this backend")
	}
	repos, err := lister.RepoList(cmd.Context())
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}

	locator, ok := rawAdapter.(store.Locator)
	if !ok {
		return fmt.Errorf("locate is not supported by this backend")
	}

	type repoResult struct {
		repo string
		resp *store.LocateResponse
		err  error
	}

	results := make(chan repoResult, len(repos))
	for _, r := range repos {
		go func(repoName string) {
			resp, err := locator.Locate(cmd.Context(), store.LocateRequest{
				Repo:  repoName,
				Query: query,
				Limit: limit,
			})
			results <- repoResult{repo: repoName, resp: resp, err: err}
		}(r)
	}

	var allResults []store.LocateResult
	var failedRepos []string
	var totalHits int
	for range repos {
		res := <-results
		if res.err != nil {
			failedRepos = append(failedRepos, res.repo)
			continue
		}
		totalHits += res.resp.Total
		for _, r := range res.resp.Results {
			// Update ref with URL-encoded repo name for round-trip safety
			r.Ref = "repo://" + url.PathEscape(res.repo) + r.Path
			allResults = append(allResults, r)
		}
	}

	// Sort by score descending
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Score > allResults[j].Score
	})

	// Apply limit to results only, Total remains pre-limit sum
	if limit > 0 && len(allResults) > limit {
		allResults = allResults[:limit]
	}

	// Report partial failures to stderr
	if len(failedRepos) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: locate failed for %d repo(s): %s\n", len(failedRepos), strings.Join(failedRepos, ", "))
	}

	// If all repos failed, return an error
	if len(failedRepos) == len(repos) {
		return fmt.Errorf("locate failed for all repos")
	}

	resp := &store.LocateResponse{Results: allResults, Total: totalHits}
	if jsonOutput {
		return printLocateJSON(cmd, resp)
	}
	return printLocateHuman(cmd, resp)
}

func printLocateHuman(cmd *cobra.Command, resp *store.LocateResponse) error {
	if len(resp.Results) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no results found")
		return nil
	}
	for _, r := range resp.Results {
		fmt.Fprintf(cmd.OutOrStdout(), "%s    [score: %.2f]\n", r.Ref, r.Score)
		if r.Snippet != "" {
			snippet := strings.ReplaceAll(r.Snippet, "**", "")
			for _, line := range strings.Split(snippet, "\n") {
				if line != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", line)
				}
			}
		}
		fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}

func printLocateJSON(cmd *cobra.Command, resp *store.LocateResponse) error {
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return nil
}
