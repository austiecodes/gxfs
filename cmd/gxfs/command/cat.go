package command

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"gxfs/internal/client"
	"gxfs/internal/store"
)

func NewCatCommand(adapter, rawAdapter store.Adapter, repo string, collectionClient *client.Client) *cobra.Command {
	var numberAll, numberNonBlank, squeezeBlanks bool

	cmd := &cobra.Command{
		Use:   "cat <path>",
		Short: "Print VFS file content",
		Long: `Print VFS file content.

Supports multiple path formats:
  - Regular path: cat /docs/readme.md
  - Repo ref: cat repo://other-repo/docs/readme.md
  - Collection ref: cat collection://my-collection/docs/readme.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			// Check for collection:// ref
			if strings.HasPrefix(path, "collection://") {
				if collectionClient == nil {
					return fmt.Errorf("collection:// refs require a configured server")
				}
				name, colPath, err := parseCollectionRef(path)
				if err != nil {
					return err
				}
				resp, err := collectionClient.GetMemberContent(cmd.Context(), name, colPath)
				if err != nil {
					return err
				}

				out := cmd.OutOrStdout()
				if !numberAll && !numberNonBlank && !squeezeBlanks {
					fmt.Fprint(out, resp.Content)
					return nil
				}

				lines := strings.Split(resp.Content, "\n")
				if len(lines) > 0 && lines[len(lines)-1] == "" {
					lines = lines[:len(lines)-1]
				}
				return printLines(out, lines, numberAll, numberNonBlank, squeezeBlanks)
			}

			// Resolve effective adapter/repo/path for repo:// refs.
			effAdapter := adapter
			effRepo := repo
			effPath := path
			if targetRepo, targetPath, ok := parseRepoRef(repo, path); ok {
				effAdapter = rawAdapter
				effRepo = targetRepo
				effPath = targetPath
			}

			resp, err := effAdapter.Cat(cmd.Context(), store.CatRequest{Repo: effRepo, Path: effPath})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if !numberAll && !numberNonBlank && !squeezeBlanks {
				fmt.Fprint(out, resp.Content)
				return nil
			}

			lines := strings.Split(resp.Content, "\n")
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			return printLines(out, lines, numberAll, numberNonBlank, squeezeBlanks)
		},
	}
	cmd.Flags().BoolVarP(&numberAll, "number", "n", false, "number all output lines")
	cmd.Flags().BoolVarP(&numberNonBlank, "number-nonblank", "b", false, "number non-blank output lines")
	cmd.Flags().BoolVarP(&squeezeBlanks, "squeeze-blank", "s", false, "squeeze multiple blank lines into one")
	return cmd
}

// parseCollectionRef parses collection://name/path into (name, path).
func parseCollectionRef(ref string) (string, string, error) {
	if !strings.HasPrefix(ref, "collection://") {
		return "", "", fmt.Errorf("invalid collection ref: must start with collection://")
	}
	rest := ref[13:] // len("collection://")
	if rest == "" {
		return "", "", fmt.Errorf("invalid collection ref: missing collection name")
	}
	// Find the first /
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return "", "", fmt.Errorf("invalid collection ref: missing path")
	}
	name := rest[:slashIdx]
	path := rest[slashIdx:]
	if name == "" {
		return "", "", fmt.Errorf("invalid collection ref: empty collection name")
	}
	if path == "" || path == "/" {
		return "", "", fmt.Errorf("invalid collection ref: missing document path")
	}
	return name, path, nil
}

func printLines(out io.Writer, lines []string, numberAll, numberNonBlank, squeezeBlanks bool) error {
	if squeezeBlanks {
		lines = squeezeBlankLines(lines)
	}

	if numberNonBlank {
		n := 0
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				fmt.Fprintln(out, line)
			} else {
				n++
				fmt.Fprintf(out, "%6d  %s\n", n, line)
			}
		}
	} else if numberAll {
		for i, line := range lines {
			fmt.Fprintf(out, "%6d  %s\n", i+1, line)
		}
	} else {
		for _, line := range lines {
			fmt.Fprintln(out, line)
		}
	}
	return nil
}

func squeezeBlankLines(lines []string) []string {
	var result []string
	prevBlank := false
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		result = append(result, line)
		prevBlank = blank
	}
	return result
}
