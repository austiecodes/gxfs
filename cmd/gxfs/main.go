package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"gxfs/internal/client"
	"gxfs/internal/config"
	"gxfs/internal/store"
)

func newRootCommand(adapter store.Adapter, repo string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gxfs",
		Short: "Inspect GXFS virtual filesystems",
		Long:  "GXFS gives agents Unix-like commands for virtual filesystem content served by gxfs-server.",
	}

	cmd.AddCommand(newLSCommand(adapter, repo))
	cmd.AddCommand(newTreeCommand(adapter, repo))
	cmd.AddCommand(newCatCommand(adapter, repo))
	cmd.AddCommand(newGrepCommand(adapter, repo))
	cmd.AddCommand(newFindCommand(adapter, repo))
	cmd.AddCommand(newStatCommand(adapter, repo))
	cmd.AddCommand(newConfigCommand(repo))
	return cmd
}

func newLSCommand(adapter store.Adapter, repo string) *cobra.Command {
	var longFmt, classify, slashDir bool
	var sortByTime, sortBySize, reverse bool
	var recursive, allFiles, dirOnly bool

	cmd := &cobra.Command{
		Use:   "ls [path]",
		Short: "List VFS directory contents",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := argPath(args, "/")

			sortField := "name"
			sortReverse := false
			if sortByTime {
				sortField = "mtime"
				sortReverse = true
			} else if sortBySize {
				sortField = "size"
				sortReverse = true
			}
			if reverse {
				sortReverse = !sortReverse
			}

			if dirOnly {
				resp, err := adapter.Stat(cmd.Context(), store.StatRequest{Repo: repo, Path: p})
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), formatLSLine(resp.Node, longFmt, classify, slashDir))
				return nil
			}

			resp, err := adapter.LS(cmd.Context(), store.LSRequest{
				Repo:      repo,
				Path:      p,
				Sort:      sortField,
				Reverse:   sortReverse,
				Recursive: recursive,
				All:       allFiles,
			})
			if err != nil {
				return err
			}
			for _, node := range resp.Nodes {
				fmt.Fprintln(cmd.OutOrStdout(), formatLSLine(node, longFmt, classify, slashDir))
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&longFmt, "long", "l", false, "use a long listing format")
	cmd.Flags().BoolVarP(&classify, "classify", "F", false, "append indicator (one of */>@)")
	cmd.Flags().BoolVarP(&slashDir, "slash", "p", false, "append / to directories")
	cmd.Flags().BoolVarP(&sortByTime, "sort-time", "t", false, "sort by modification time (newest first)")
	cmd.Flags().BoolVarP(&sortBySize, "sort-size", "S", false, "sort by file size (largest first)")
	cmd.Flags().BoolVarP(&reverse, "reverse", "r", false, "reverse the sort order")
	cmd.Flags().BoolVarP(&recursive, "recursive", "R", false, "list subdirectories recursively")
	cmd.Flags().BoolVarP(&allFiles, "all", "a", false, "show hidden files")
	cmd.Flags().BoolVarP(&dirOnly, "directory", "d", false, "list directory entries instead of contents")
	return cmd
}

func formatLSLine(node store.Node, long, classify, slashDir bool) string {
	isDir := node.Kind == "dir"

	if !long {
		name := node.Name
		if isDir {
			name += "/"
		}
		return name
	}

	kindIndicator := "-rw-"
	if isDir {
		kindIndicator = "drwx"
	}

	size := "       -"
	if !isDir {
		size = fmt.Sprintf("%8d", node.Size)
	}

	modTime := "-"
	if node.ModTime != "" {
		modTime = node.ModTime
	}

	suffix := ""
	if isDir && (classify || slashDir) {
		suffix = "/"
	}

	return fmt.Sprintf("%s  %s  %s  %s%s", kindIndicator, size, modTime, node.Name, suffix)
}

func newTreeCommand(adapter store.Adapter, repo string) *cobra.Command {
	var depth int
	var treeAll, treeDirsOnly, treeFullPath, treeShowSize, treeSortByTime, treeDirsFirst bool
	cmd := &cobra.Command{
		Use:   "tree [path]",
		Short: "Print a VFS tree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sortField := ""
			if treeSortByTime {
				sortField = "mtime"
			}
			resp, err := adapter.Tree(cmd.Context(), store.TreeRequest{
				Repo:      repo,
				Path:      argPath(args, "/"),
				Depth:     depth,
				All:       treeAll,
				DirsOnly:  treeDirsOnly,
				FullPath:  treeFullPath,
				ShowSize:  treeShowSize,
				Sort:      sortField,
				DirsFirst: treeDirsFirst,
			})
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), resp.Text)
			return nil
		},
	}
	cmd.Flags().IntVarP(&depth, "level", "L", 2, "maximum tree depth")
	cmd.Flags().BoolVarP(&treeAll, "all", "a", false, "show hidden files")
	cmd.Flags().BoolVarP(&treeDirsOnly, "dirs-only", "d", false, "list directories only")
	cmd.Flags().BoolVarP(&treeFullPath, "full-path", "f", false, "print full path prefix")
	cmd.Flags().BoolVarP(&treeShowSize, "size", "s", false, "show file sizes")
	cmd.Flags().BoolVarP(&treeSortByTime, "sort-time", "t", false, "sort by modification time")
	cmd.Flags().BoolVar(&treeDirsFirst, "dirsfirst", false, "list directories before files")
	return cmd
}

func newCatCommand(adapter store.Adapter, repo string) *cobra.Command {
	var numberAll, numberNonBlank, squeezeBlanks bool

	cmd := &cobra.Command{
		Use:   "cat <path>",
		Short: "Print VFS file content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adapter.Cat(cmd.Context(), store.CatRequest{Repo: repo, Path: args[0]})
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
		},
	}
	cmd.Flags().BoolVarP(&numberAll, "number", "n", false, "number all output lines")
	cmd.Flags().BoolVarP(&numberNonBlank, "number-nonblank", "b", false, "number non-blank output lines")
	cmd.Flags().BoolVarP(&squeezeBlanks, "squeeze-blank", "s", false, "squeeze multiple blank lines into one")
	return cmd
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

func newGrepCommand(adapter store.Adapter, repo string) *cobra.Command {
	var regex, countOnly, filesOnly, onlyMatch bool
	var caseInsensitive, invert, wholeWord, wholeLine bool
	var contextAfter, contextBefore, context int
	var allFiles bool
	var includePattern, excludePattern string

	cmd := &cobra.Command{
		Use:   "grep <pattern> [path]",
		Short: "Search VFS files",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pattern := args[0]

			before := contextBefore
			after := contextAfter
			if context > 0 {
				if before == 0 {
					before = context
				}
				if after == 0 {
					after = context
				}
			}

			resp, err := adapter.Grep(cmd.Context(), store.GrepRequest{
				Repo:            repo,
				Pattern:         pattern,
				Path:            argPath(args[1:], "/"),
				Regex:           regex,
				CaseInsensitive: caseInsensitive,
				Invert:          invert,
				WholeWord:       wholeWord,
				WholeLine:       wholeLine,
				ContextBefore:   before,
				ContextAfter:    after,
				All:             allFiles,
				Include:         includePattern,
				Exclude:         excludePattern,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if filesOnly {
				seen := map[string]bool{}
				for _, match := range resp.Matches {
					if !seen[match.Path] {
						fmt.Fprintln(out, match.Path)
						seen[match.Path] = true
					}
				}
				return nil
			}

			if countOnly {
				counts := map[string]int{}
				for _, match := range resp.Matches {
					counts[match.Path]++
				}
				for _, match := range resp.Matches {
					if counts[match.Path] >= 0 {
						fmt.Fprintf(out, "%s:%d\n", match.Path, counts[match.Path])
						counts[match.Path] = -1
					}
				}
				return nil
			}

			if onlyMatch && !regex {
				for _, match := range resp.Matches {
					remaining := match.Text
					for {
						idx := strings.Index(remaining, pattern)
						if idx < 0 {
							break
						}
						fmt.Fprintf(out, "%s:%d:%s\n", match.Path, match.Line, pattern)
						remaining = remaining[idx+len(pattern):]
					}
				}
				return nil
			}

			for _, match := range resp.Matches {
				for _, line := range match.Before {
					fmt.Fprintf(out, "%s-%s\n", match.Path, line)
				}
				fmt.Fprintf(out, "%s:%d:%s\n", match.Path, match.Line, match.Text)
				for _, line := range match.After {
					fmt.Fprintf(out, "%s-%s\n", match.Path, line)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&regex, "regex", "E", false, "treat pattern as a regular expression")
	cmd.Flags().BoolVarP(&countOnly, "count", "c", false, "print only a count of matching lines per file")
	cmd.Flags().BoolVarP(&filesOnly, "files-with-matches", "l", false, "print only names of files with matches")
	cmd.Flags().BoolVarP(&onlyMatch, "only-matching", "o", false, "show only the matching part of the line")
	cmd.Flags().BoolVarP(&caseInsensitive, "ignore-case", "i", false, "case-insensitive search")
	cmd.Flags().BoolVarP(&invert, "invert-match", "v", false, "show non-matching lines")
	cmd.Flags().BoolVarP(&wholeWord, "word-regexp", "w", false, "match whole words only")
	cmd.Flags().BoolVarP(&wholeLine, "line-regexp", "x", false, "match whole lines only")
	cmd.Flags().IntVarP(&contextAfter, "after-context", "A", 0, "lines of trailing context")
	cmd.Flags().IntVarP(&contextBefore, "before-context", "B", 0, "lines of leading context")
	cmd.Flags().IntVarP(&context, "context", "C", 0, "lines of context (before and after)")
	cmd.Flags().BoolVarP(&allFiles, "all", "a", false, "search hidden files")
	cmd.Flags().StringVar(&includePattern, "include", "", "only search files matching glob")
	cmd.Flags().StringVar(&excludePattern, "exclude", "", "skip files matching glob")
	return cmd
}

func newFindCommand(adapter store.Adapter, repo string) *cobra.Command {
	var name, findType, iname string
	var maxDepth, minDepth int
	var allFiles bool
	cmd := &cobra.Command{
		Use:   "find [path] -name <glob>",
		Short: "Find VFS files by name",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && iname == "" {
				return fmt.Errorf("-name or -iname is required")
			}
			switch findType {
			case "f":
				findType = "file"
			case "d":
				findType = "dir"
			case "", "file", "dir":
				// valid as-is
			default:
				return fmt.Errorf("invalid type %q: use f or d", findType)
			}
			resp, err := adapter.Find(cmd.Context(), store.FindRequest{
				Repo:     repo,
				Path:     argPath(args, "/"),
				Name:     name,
				Type:     findType,
				MaxDepth: maxDepth,
				MinDepth: minDepth,
				All:      allFiles,
				IName:    iname,
			})
			if err != nil {
				return err
			}
			for _, node := range resp.Nodes {
				fmt.Fprintln(cmd.OutOrStdout(), node.Path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "filename glob")
	cmd.Flags().StringVarP(&findType, "type", "t", "", "filter by type (f=file, d=directory)")
	cmd.Flags().IntVar(&maxDepth, "maxdepth", 0, "maximum descent depth (0 = unlimited)")
	cmd.Flags().IntVar(&minDepth, "mindepth", 0, "minimum descent depth")
	cmd.Flags().BoolVarP(&allFiles, "all", "a", false, "search hidden files")
	cmd.Flags().StringVar(&iname, "iname", "", "case-insensitive name glob")
	return cmd
}

func newStatCommand(adapter store.Adapter, repo string) *cobra.Command {
	var customFormat string
	var terse bool

	cmd := &cobra.Command{
		Use:   "stat <path>",
		Short: "Print VFS node metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adapter.Stat(cmd.Context(), store.StatRequest{Repo: repo, Path: args[0]})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			node := resp.Node

			if customFormat != "" {
				fmt.Fprintln(out, expandStatFormat(customFormat, node))
				return nil
			}

			if terse {
				modTime := "-"
				if node.ModTime != "" {
					modTime = node.ModTime
				}
				fmt.Fprintf(out, "%s\t%s\t%d\t%s\n", node.Path, node.Kind, node.Size, modTime)
				return nil
			}

			fmt.Fprintf(out, "  Path: %s\n", node.Path)
			fmt.Fprintf(out, "  Name: %s\n", node.Name)
			fmt.Fprintf(out, "  Kind: %s\n", node.Kind)
			fmt.Fprintf(out, "  Size: %d\n", node.Size)
			modTime := "-"
			if node.ModTime != "" {
				modTime = node.ModTime
			}
			fmt.Fprintf(out, "  ModTime: %s\n", modTime)
			if len(node.Meta) > 0 {
				fmt.Fprintf(out, "  Meta: %s\n", formatMeta(node.Meta))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&customFormat, "format", "c", "", "custom format string")
	cmd.Flags().BoolVarP(&terse, "terse", "f", false, "terse single-line format")
	return cmd
}

func expandStatFormat(format string, node store.Node) string {
	s := format
	s = strings.ReplaceAll(s, "%%", "\x00")
	s = strings.ReplaceAll(s, "%n", node.Name)
	s = strings.ReplaceAll(s, "%p", node.Path)
	s = strings.ReplaceAll(s, "%k", node.Kind)
	s = strings.ReplaceAll(s, "%s", fmt.Sprintf("%d", node.Size))
	modTime := "-"
	if node.ModTime != "" {
		modTime = node.ModTime
	}
	s = strings.ReplaceAll(s, "%y", modTime)
	meta := formatMeta(node.Meta)
	s = strings.ReplaceAll(s, "%m", meta)
	s = strings.ReplaceAll(s, "\x00", "%")
	return s
}

func formatMeta(meta map[string]string) string {
	if len(meta) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(meta))
	for k, v := range meta {
		pairs = append(pairs, k+"="+v)
	}
	return strings.Join(pairs, ", ")
}

func newConfigCommand(repo string) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect GXFS CLI configuration",
	}
	configCmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check resolved GXFS CLI configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "Project: %s\n", repo)
			return nil
		},
	})
	return configCmd
}

func argPath(args []string, fallback string) string {
	if len(args) == 0 || args[0] == "" {
		return fallback
	}
	return args[0]
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if wantsHelp(args) {
		cmd := newRootCommand(nil, "")
		cmd.SetArgs(args)
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		if err := cmd.Execute(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	path := os.Getenv("GXFS_CONFIG")
	if path == "" {
		path = "gxfs.toml"
	}
	cfg, err := config.LoadCLI(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	cmd := newRootCommand(client.New(cfg.Server.Addr), cfg.Project)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "help" || arg == "--help" || arg == "-h" || strings.HasSuffix(arg, " --help") {
			return true
		}
	}
	return false
}
