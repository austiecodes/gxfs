package command

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/austiecodes/rolio/internal/client"
	"github.com/austiecodes/rolio/internal/store"
)

func NewCatCommand(adapter, rawAdapter store.Adapter, repo string, docsetClient *client.Client) *cobra.Command {
	var numberAll, numberNonBlank, squeezeBlanks bool

	cmd := &cobra.Command{
		Use:   "cat <path>",
		Short: "Print VFS file content",
		Long: `Print VFS file content.

Supports multiple path formats:
  - Regular path: cat /docs/readme.md
  - Repo ref: cat repo://other-repo/docs/readme.md
  - Docset ref: cat docset://my-docset/docs/readme.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			// Check for docset:// ref
			if strings.HasPrefix(path, "docset://") {
				if docsetClient == nil {
					return fmt.Errorf("docset:// refs require a configured server")
				}
				name, docsetPath, err := parseDocsetRef(path)
				if err != nil {
					return err
				}
				resp, err := docsetClient.GetDocsetMemberContent(cmd.Context(), name, docsetPath)
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

// parseDocsetRef parses docset://name/path into (name, path).
func parseDocsetRef(ref string) (string, string, error) {
	if !strings.HasPrefix(ref, "docset://") {
		return "", "", fmt.Errorf("invalid docset ref: must start with docset://")
	}
	rest := strings.TrimPrefix(ref, "docset://")
	if rest == "" {
		return "", "", fmt.Errorf("invalid docset ref: missing docset name")
	}
	// Find the first /
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return "", "", fmt.Errorf("invalid docset ref: missing path")
	}
	name := rest[:slashIdx]
	path := rest[slashIdx:]
	if name == "" {
		return "", "", fmt.Errorf("invalid docset ref: empty docset name")
	}
	if path == "" || path == "/" {
		return "", "", fmt.Errorf("invalid docset ref: missing document path")
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
