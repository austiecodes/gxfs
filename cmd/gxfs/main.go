package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sync/errgroup"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"

	"gxfs/internal/client"
	"gxfs/internal/config"
	mountadapter "gxfs/internal/mount"
	"gxfs/internal/store"
	"gxfs/internal/syncmanifest"
)

func newRootCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gxfs",
		Short: "Inspect GXFS virtual filesystems",
		Long:  "GXFS gives agents Unix-like commands for virtual filesystem content served by gxfs-server.",
	}

	cmd.AddCommand(newLSCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(newTreeCommand(adapter, repo))
	cmd.AddCommand(newCatCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(newGrepCommand(adapter, repo))
	cmd.AddCommand(newFindCommand(adapter, repo))
	cmd.AddCommand(newStatCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(newWriteCommand(adapter, repo))
	cmd.AddCommand(newEditCommand(adapter, repo))
	cmd.AddCommand(newDeleteCommand(adapter, repo))
	cmd.AddCommand(newSearchCommand(adapter, repo))
	cmd.AddCommand(newRefreshCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(newMaterializeCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(newDematerializeCommand())
	cmd.AddCommand(newInitCommand())
	cmd.AddCommand(newConfigCommand(repo))
	cmd.AddCommand(newSyncCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(newMountCommand(adapter, rawAdapter, repo))
	return cmd
}

func newLSCommand(adapter, rawAdapter store.Adapter, repo string) *cobra.Command {
	var longFmt, classify, slashDir bool
	var sortByTime, sortBySize, reverse bool
	var recursive, allFiles, dirOnly bool
	var lsLimit, lsOffset int

	cmd := &cobra.Command{
		Use:   "ls [path]",
		Short: "List VFS directory contents",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateNonNeg([]string{"limit", "offset"}, lsLimit, lsOffset); err != nil {
				return err
			}
			p := argPath(args, "/")

			// Resolve effective adapter, repo, and path. For repo:// refs,
			// use rawAdapter directly; otherwise use the normal mount adapter.
			effAdapter := adapter
			effRepo := repo
			if targetRepo, targetPath, ok := parseRepoRef(p); ok {
				effAdapter = rawAdapter
				effRepo = targetRepo
				p = targetPath
			}

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
				resp, err := effAdapter.Stat(cmd.Context(), store.StatRequest{Repo: effRepo, Path: p})
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), formatLSLine(resp.Node, longFmt, classify, slashDir))
				return nil
			}

			resp, err := effAdapter.LS(cmd.Context(), store.LSRequest{
				Repo:      repo,
				Path:      p,
				Sort:      sortField,
				Reverse:   sortReverse,
				Recursive: recursive,
				All:       allFiles,
				Limit:     lsLimit,
				Offset:    lsOffset,
			})
			if err != nil {
				return err
			}
			for _, node := range resp.Nodes {
				fmt.Fprintln(cmd.OutOrStdout(), formatLSLine(node, longFmt, classify, slashDir))
			}
			printPaginationSummary(cmd.OutOrStdout(), lsOffset, len(resp.Nodes), resp.Total)
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
	cmd.Flags().IntVar(&lsLimit, "limit", 0, "max results (0 = unlimited)")
	cmd.Flags().IntVar(&lsOffset, "offset", 0, "skip first N results")
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

func newCatCommand(adapter, rawAdapter store.Adapter, repo string) *cobra.Command {
	var numberAll, numberNonBlank, squeezeBlanks bool

	cmd := &cobra.Command{
		Use:   "cat <path>",
		Short: "Print VFS file content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve effective adapter/repo/path for repo:// refs.
			effAdapter := adapter
			effRepo := repo
			effPath := args[0]
			if targetRepo, targetPath, ok := parseRepoRef(args[0]); ok {
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
				if !cmd.Flags().Changed("before-context") {
					before = context
				}
				if !cmd.Flags().Changed("after-context") {
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
	var maxDepth, minDepth, findLimit, findOffset int
	var allFiles bool
	cmd := &cobra.Command{
		Use:   "find [path] -name <glob>",
		Short: "Find VFS files by name",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateNonNeg([]string{"limit", "offset"}, findLimit, findOffset); err != nil {
				return err
			}
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
				Limit:    findLimit,
				Offset:   findOffset,
			})
			if err != nil {
				return err
			}
			for _, node := range resp.Nodes {
				fmt.Fprintln(cmd.OutOrStdout(), node.Path)
			}
			printPaginationSummary(cmd.OutOrStdout(), findOffset, len(resp.Nodes), resp.Total)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "filename glob")
	cmd.Flags().StringVarP(&findType, "type", "t", "", "filter by type (f=file, d=directory)")
	cmd.Flags().IntVar(&maxDepth, "maxdepth", 0, "maximum descent depth (0 = unlimited)")
	cmd.Flags().IntVar(&minDepth, "mindepth", 0, "minimum descent depth")
	cmd.Flags().BoolVarP(&allFiles, "all", "a", false, "search hidden files")
	cmd.Flags().StringVar(&iname, "iname", "", "case-insensitive name glob")
	cmd.Flags().IntVar(&findLimit, "limit", 0, "max results (0 = unlimited)")
	cmd.Flags().IntVar(&findOffset, "offset", 0, "skip first N results")
	return cmd
}

func newStatCommand(adapter, rawAdapter store.Adapter, repo string) *cobra.Command {
	var customFormat string
	var terse bool

	cmd := &cobra.Command{
		Use:   "stat <path>",
		Short: "Print VFS node metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve effective adapter/repo/path for repo:// refs.
			effAdapter := adapter
			effRepo := repo
			effPath := args[0]
			if targetRepo, targetPath, ok := parseRepoRef(args[0]); ok {
				effAdapter = rawAdapter
				effRepo = targetRepo
				effPath = targetPath
			}

			resp, err := effAdapter.Stat(cmd.Context(), store.StatRequest{Repo: effRepo, Path: effPath})
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

func newWriteCommand(adapter store.Adapter, repo string) *cobra.Command {
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

			resp, err := adapter.Put(cmd.Context(), store.PutRequest{
				Repo:    repo,
				Path:    path,
				Content: content,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes)\n", resp.Node.Path, resp.Node.Size)
			return nil
		},
	}
	return cmd
}

func newDeleteCommand(adapter store.Adapter, repo string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <path>",
		Short: "Delete a VFS file or directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := adapter.Delete(cmd.Context(), store.DeleteRequest{
				Repo: repo,
				Path: args[0],
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func newSearchCommand(adapter store.Adapter, repo string) *cobra.Command {
	var searchPath string
	var limit, searchOffset int
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search documents by keyword",
		Long:  "Search documents by keyword using full-text search.\nReturns ranked results with relevance scores and matching snippets.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateNonNeg([]string{"limit", "offset"}, limit, searchOffset); err != nil {
				return err
			}
			resp, err := adapter.Search(cmd.Context(), store.SearchRequest{
				Repo:   repo,
				Query:  args[0],
				Path:   searchPath,
				Limit:  limit,
				Offset: searchOffset,
			})
			if err != nil {
				return err
			}
			if jsonOutput {
				return printSearchJSON(cmd, resp)
			}
			return printSearchHuman(cmd, resp, searchOffset)
		},
	}
	cmd.Flags().StringVar(&searchPath, "path", "", "scope search to path prefix")
	cmd.Flags().IntVar(&limit, "limit", 20, "max results")
	cmd.Flags().IntVar(&searchOffset, "offset", 0, "skip first N results")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func printSearchHuman(cmd *cobra.Command, resp *store.SearchResponse, offset int) error {
	if len(resp.Results) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no results found")
		return nil
	}
	for _, r := range resp.Results {
		sizeStr := formatSize(r.Size)
		fmt.Fprintf(cmd.OutOrStdout(), "%s    [rank: %.2f, %s]\n", r.Path, r.Rank, sizeStr)
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
	printPaginationSummary(cmd.OutOrStdout(), offset, len(resp.Results), resp.Total)
	return nil
}

func printSearchJSON(cmd *cobra.Command, resp *store.SearchResponse) error {
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return nil
}

func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
}

func newEditCommand(adapter store.Adapter, repo string) *cobra.Command {
	var old, newStr string
	var all bool

	cmd := &cobra.Command{
		Use:   "edit <path> --old <text> --new <text> [--all]",
		Short: "Replace text in a VFS file",
		Long:  "Replace occurrences of old text with new text in a VFS file.\nBy default replaces only the first occurrence. Use --all to replace all.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adapter.Edit(cmd.Context(), store.EditRequest{
				Repo: repo,
				Path: args[0],
				Old:  old,
				New:  newStr,
				All:  all,
			})
			if err != nil {
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

func newSyncCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize local docs with GXFS",
	}
	syncCmd.AddCommand(newSyncPushCommand(adapter, repo, resolver))
	syncCmd.AddCommand(newSyncPullCommand(adapter, rawAdapter, repo, resolver))
	return syncCmd
}

func newSyncPushCommand(adapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var manifestPath string
	cmd := &cobra.Command{
		Use:   "push <local-path>",
		Short: "Push local docs into GXFS",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			files, err := syncmanifest.ScanLocal(root)
			if err != nil {
				return err
			}

			entries := make([]syncmanifest.Entry, 0, len(files))
			for _, file := range files {
				resp, err := adapter.Put(cmd.Context(), store.PutRequest{
					Repo:    repo,
					Path:    file.LocalPath,
					Content: file.Content,
				})
				if err != nil {
					return err
				}
				entries = append(entries, syncmanifest.Entry{
					Local:        file.LocalPath,
					RemoteDoc:    resolveRemoteDoc(resolver, repo, file.LocalPath, resp.Node.Path),
					Mount:        cleanSyncMount(root),
					ContentHash:  file.ContentHash,
					Size:         file.Size,
					MTime:        file.MTime.Format(time.RFC3339),
					Materialized: true,
				})
			}

			if manifestPath == "" {
				manifestPath = filepath.Join(".gxfs", "manifest.toml")
			}
			manifest := syncmanifest.Manifest{}
			if existing, err := syncmanifest.Load(manifestPath); err == nil {
				manifest = existing
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			manifest = syncmanifest.Upsert(manifest, entries)
			if err := syncmanifest.Save(manifestPath, manifest); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "pushed %d file%s from %s\n", len(files), plural(len(files)), root)
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	return cmd
}

func cleanSyncMount(root string) string {
	root = filepath.ToSlash(filepath.Clean(root))
	root = strings.Trim(root, "/")
	if root == "." {
		return ""
	}
	return root
}

// resolveRemoteDoc returns the correct remote_doc reference for a file.
// When a mount resolver is available, it resolves the local path to get the
// true remote repo and path. Otherwise it falls back to the response node path.
func resolveRemoteDoc(resolver *mountadapter.Resolver, repo, localPath, fallbackPath string) string {
	if resolver != nil {
		if resolved, err := resolver.Resolve(localPath, mountadapter.OpWrite); err == nil {
			return "repo://" + resolved.RemoteRepo + "/" + strings.Trim(resolved.RemotePath, "/")
		}
	}
	return "repo://" + repo + "/" + strings.Trim(fallbackPath, "/")
}
func newSyncPullCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var manifestPath string
	var materialize bool
	var forceLocal bool
	var forceRemote bool
	cmd := &cobra.Command{
		Use:   "pull <local-path>",
		Short: "Pull GXFS docs into the local sync manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if forceLocal && forceRemote {
				return fmt.Errorf("--force-local and --force-remote are mutually exclusive")
			}
			root := args[0]
			if manifestPath == "" {
				manifestPath = filepath.Join(".gxfs", "manifest.toml")
			}

			manifest, err := loadManifest(manifestPath)
			if err != nil {
				return err
			}

			plan, err := buildRemoteSyncPlanForRoot(cmd.Context(), adapter, rawAdapter, resolver, repo, root, manifest, remoteSyncOptions{
				Materialize: materialize,
				ForceLocal:  forceLocal,
				ForceRemote: forceRemote,
			})
			if err != nil {
				return err
			}
			result, err := applyRemoteSyncPlan(cmd.Context(), adapter, rawAdapter, repo, root, manifestPath, manifest, plan, resolver)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "pulled %d file%s from %s\n", result.RemoteFiles, plural(result.RemoteFiles), root)
			if materialize {
				fmt.Fprintf(cmd.OutOrStdout(), "materialized %d file%s under %s\n", result.Materialized, plural(result.Materialized), root)
			}
			if result.ForcePushed > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "pushed %d local file%s to resolve conflicts\n", result.ForcePushed, plural(result.ForcePushed))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	cmd.Flags().BoolVar(&materialize, "materialize", false, "write pulled docs to local files")
	cmd.Flags().BoolVar(&forceLocal, "force-local", false, "resolve conflicts by pushing local content")
	cmd.Flags().BoolVar(&forceRemote, "force-remote", false, "resolve conflicts by accepting remote content")
	return cmd
}

type remoteSyncFile struct {
	LocalPath   string
	RemoteRepo  string
	RemotePath  string
	Content     string
	ContentHash string
	Size        int64
	MTime       string
}

type localSyncFile struct {
	LocalPath   string
	Content     string
	ContentHash string
	Size        int64
	MTime       time.Time
}

type remoteSyncOptions struct {
	Materialize bool
	ForceLocal  bool
	ForceRemote bool
}

type remoteSyncAction int

const (
	remoteSyncAccept remoteSyncAction = iota
	remoteSyncMaterialize
	remoteSyncPushLocal
)

type remoteSyncPlan struct {
	Changes []remoteSyncChange
}

type remoteSyncChange struct {
	Action remoteSyncAction
	Remote remoteSyncFile
	Local  *localSyncFile
	Entry  syncmanifest.Entry
}

type remoteSyncResult struct {
	Manifest     syncmanifest.Manifest
	RemoteFiles  int
	Materialized int
	ForcePushed  int
}

// fetchFileContents fetches file content for a slice of file nodes using
// bounded concurrent Cat calls, producing remoteSyncFiles at stable indices.
func fetchFileContents(ctx context.Context, adapter store.Adapter, repo string, fileNodes []store.Node, localPathFn func(store.Node) string) ([]remoteSyncFile, error) {
	files := make([]remoteSyncFile, len(fileNodes))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)

	for i, node := range fileNodes {
		i, node := i, node
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			cat, err := adapter.Cat(gctx, store.CatRequest{Repo: repo, Path: node.Path})
			if err != nil {
				return err
			}
			files[i] = remoteSyncFile{
				LocalPath:   localPathFn(node),
				RemoteRepo:  repo,
				RemotePath:  node.Path,
				Content:     cat.Content,
				ContentHash: store.HashContent(cat.Content),
				Size:        int64(len(cat.Content)),
				MTime:       node.ModTime,
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return files, nil
}

// collectRemoteFilesMetadata fetches file metadata + known hashes without content.
// Returns file nodes and a map of path->hash for files with known hashes.
func collectRemoteFilesMetadata(ctx context.Context, adapter store.Adapter, repo, root string) ([]store.Node, map[string]string, error) {
	stat, err := adapter.Stat(ctx, store.StatRequest{Repo: repo, Path: root})
	if err != nil {
		return nil, nil, err
	}

	nodes := []store.Node{stat.Node}
	if stat.Node.Kind == "dir" {
		resp, err := adapter.LS(ctx, store.LSRequest{Repo: repo, Path: root, Recursive: true, All: true})
		if err != nil {
			return nil, nil, err
		}
		nodes = resp.Nodes
	}

	var fileNodes []store.Node
	for _, node := range nodes {
		if node.Kind == "file" {
			fileNodes = append(fileNodes, node)
		}
	}

	// Fetch known hashes in one query
	hashResp, err := adapter.BatchHashes(ctx, store.HashRequest{Repo: repo, Path: root})
	if err != nil {
		return nil, nil, fmt.Errorf("batch hashes: %w", err)
	}
	hashMap := make(map[string]string, len(hashResp.Hashes))
	for _, ch := range hashResp.Hashes {
		hashMap[ch.Path] = ch.Hash
	}

	return fileNodes, hashMap, nil
}

// fetchChangedFileContents Cats only the files that need content (hash unknown or changed).
// For unchanged files, returns a remoteSyncFile with Content="" and the known hash.
func fetchChangedFileContents(ctx context.Context, adapter store.Adapter, repo string, fileNodes []store.Node, hashMap map[string]string, manifest syncmanifest.Manifest, localPathFn func(store.Node) string) ([]remoteSyncFile, error) {
	existingByLocal := manifestEntriesByLocal(manifest)

	// Determine which files need Cat
	var needCat []int // indices into fileNodes
	files := make([]remoteSyncFile, len(fileNodes))
	for i, node := range fileNodes {
		localPath := localPathFn(node)
		hash, hashKnown := hashMap[node.Path]
		existing, hasExisting := existingByLocal[localPath]

		if hashKnown && hasExisting && existing.ContentHash == hash {
			// Unchanged — no Cat needed
			files[i] = remoteSyncFile{
				LocalPath:   localPath,
				RemoteRepo:  repo,
				RemotePath:  node.Path,
				ContentHash: hash,
				Size:        node.Size,
				MTime:       node.ModTime,
			}
			continue
		}
		needCat = append(needCat, i)
	}

	// Cat only changed/unknown files
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, idx := range needCat {
		i := idx
		node := fileNodes[i]
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			cat, err := adapter.Cat(gctx, store.CatRequest{Repo: repo, Path: node.Path})
			if err != nil {
				return err
			}
			files[i] = remoteSyncFile{
				LocalPath:   localPathFn(node),
				RemoteRepo:  repo,
				RemotePath:  node.Path,
				Content:     cat.Content,
				ContentHash: store.HashContent(cat.Content),
				Size:        int64(len(cat.Content)),
				MTime:       node.ModTime,
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return files, nil
}

func collectRemoteFiles(ctx context.Context, adapter store.Adapter, repo, root string, manifest syncmanifest.Manifest) ([]remoteSyncFile, error) {
	fileNodes, hashMap, err := collectRemoteFilesMetadata(ctx, adapter, repo, root)
	if err != nil {
		return nil, err
	}
	return fetchChangedFileContents(ctx, adapter, repo, fileNodes, hashMap, manifest, func(node store.Node) string {
		return strings.Trim(strings.TrimPrefix(filepath.ToSlash(node.Path), "./"), "/")
	})
}

// collectMountedRemoteFiles collects remote files for a mounted path by resolving
// the local root through the mount resolver, fetching from the raw adapter using
// the real remote path, then mapping node paths back to local paths.
// This ensures RemotePath contains the true server path for correct remote_doc
// in the manifest, rather than the localized display path.
func collectMountedRemoteFiles(ctx context.Context, rawAdapter store.Adapter, resolver *mountadapter.Resolver, repo, localRoot string, manifest syncmanifest.Manifest) ([]remoteSyncFile, error) {
	resolved, err := resolver.Resolve(localRoot, mountadapter.OpRead)
	if err != nil {
		return nil, fmt.Errorf("resolve mount %s: %w", localRoot, err)
	}
	remoteRepo := resolved.RemoteRepo
	stat, err := rawAdapter.Stat(ctx, store.StatRequest{Repo: remoteRepo, Path: resolved.RemotePath})
	if err != nil {
		return nil, err
	}
	nodes := []store.Node{stat.Node}
	if stat.Node.Kind == "dir" {
		resp, err := rawAdapter.LS(ctx, store.LSRequest{Repo: remoteRepo, Path: resolved.RemotePath, Recursive: true, All: true})
		if err != nil {
			return nil, err
		}
		nodes = resp.Nodes
	}

	var fileNodes []store.Node
	for _, node := range nodes {
		if node.Kind == "file" {
			fileNodes = append(fileNodes, node)
		}
	}

	remoteRoot := strings.TrimSuffix(resolved.RemotePath, "/")
	localBase := resolved.LocalPath

	// Fetch known hashes for hash-skip optimization
	hashResp, err := rawAdapter.BatchHashes(ctx, store.HashRequest{Repo: remoteRepo, Path: resolved.RemotePath})
	if err != nil {
		return nil, fmt.Errorf("batch hashes: %w", err)
	}
	hashMap := make(map[string]string, len(hashResp.Hashes))
	for _, ch := range hashResp.Hashes {
		hashMap[ch.Path] = ch.Hash
	}

	localPathFn := func(node store.Node) string {
		rel := strings.TrimPrefix(node.Path, remoteRoot+"/")
		rel = strings.TrimPrefix(rel, remoteRoot)
		localPath := localBase
		if rel != "" {
			localPath = localBase + "/" + rel
		}
		return localPath
	}
	return fetchChangedFileContents(ctx, rawAdapter, remoteRepo, fileNodes, hashMap, manifest, localPathFn)
}

func buildRemoteSyncPlan(ctx context.Context, adapter store.Adapter, repo, root string, manifest syncmanifest.Manifest, opts remoteSyncOptions) (remoteSyncPlan, error) {
	remoteFiles, err := collectRemoteFiles(ctx, adapter, repo, root, manifest)
	if err != nil {
		return remoteSyncPlan{}, err
	}
	return buildRemoteSyncPlanFromFiles(remoteFiles, manifest, opts, root)
}

// buildRemoteSyncPlanForRoot picks the correct collection strategy based on
// whether a mount resolver is available, then builds the sync plan.
// When resolver != nil, it uses the raw adapter + resolver so that RemotePath
// contains the true server path (not the localized display path).
func buildRemoteSyncPlanForRoot(ctx context.Context, adapter, rawAdapter store.Adapter, resolver *mountadapter.Resolver, repo, root string, manifest syncmanifest.Manifest, opts remoteSyncOptions) (remoteSyncPlan, error) {
	if resolver != nil {
		remoteFiles, err := collectMountedRemoteFiles(ctx, rawAdapter, resolver, repo, root, manifest)
		if err != nil {
			return remoteSyncPlan{}, err
		}
		return buildRemoteSyncPlanFromFiles(remoteFiles, manifest, opts, root)
	}
	return buildRemoteSyncPlan(ctx, adapter, repo, root, manifest, opts)
}

func buildRemoteSyncPlanFromFiles(remoteFiles []remoteSyncFile, manifest syncmanifest.Manifest, opts remoteSyncOptions, root string) (remoteSyncPlan, error) {
	existingByLocal := manifestEntriesByLocal(manifest)
	changes := make([]remoteSyncChange, 0, len(remoteFiles))
	for _, remote := range remoteFiles {
		existing, hasExisting := existingByLocal[remote.LocalPath]
		local, localExists, err := readLocalSyncFile(remote.LocalPath)
		if err != nil {
			return remoteSyncPlan{}, err
		}

		remoteChanged := !hasExisting || existing.ContentHash != remote.ContentHash
		localChanged := localExists && hasExisting && local.ContentHash != existing.ContentHash
		untrackedLocalConflict := opts.Materialize && localExists && !hasExisting && local.ContentHash != remote.ContentHash

		if localChanged && remoteChanged {
			if opts.ForceLocal {
				changes = append(changes, remoteSyncChange{Action: remoteSyncPushLocal, Remote: remote, Local: &local})
				continue
			}
			if !opts.ForceRemote {
				return remoteSyncPlan{}, fmt.Errorf("conflict on %s: local and remote both changed (use --force-local or --force-remote)", remote.LocalPath)
			}
		}
		if opts.Materialize && (localChanged || untrackedLocalConflict) && !opts.ForceRemote {
			if opts.ForceLocal && localExists {
				changes = append(changes, remoteSyncChange{Action: remoteSyncPushLocal, Remote: remote, Local: &local})
				continue
			}
			return remoteSyncPlan{}, fmt.Errorf("local file %s has unpushed changes (use --force-local or --force-remote)", remote.LocalPath)
		}

		action := remoteSyncAccept
		entry := remote.toManifestEntry(root, false)
		if opts.Materialize {
			action = remoteSyncMaterialize
			entry.Materialized = true
		} else if localExists && local.ContentHash == remote.ContentHash {
			entry.Materialized = true
		}
		changes = append(changes, remoteSyncChange{Action: action, Remote: remote, Entry: entry})
	}
	return remoteSyncPlan{Changes: changes}, nil
}

func applyRemoteSyncPlan(ctx context.Context, adapter store.Adapter, rawAdapter store.Adapter, repo, root, manifestPath string, manifest syncmanifest.Manifest, plan remoteSyncPlan, resolver *mountadapter.Resolver) (remoteSyncResult, error) {
	result := remoteSyncResult{RemoteFiles: len(plan.Changes)}
	entries := make([]syncmanifest.Entry, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		switch change.Action {
		case remoteSyncAccept:
			entries = append(entries, change.Entry)
		case remoteSyncPushLocal:
			entry, err := pushLocalConflict(ctx, adapter, repo, *change.Local, change.Remote, root, resolver)
			if err != nil {
				return remoteSyncResult{}, err
			}
			entries = append(entries, entry)
			result.ForcePushed++
		case remoteSyncMaterialize:
			content := change.Remote.Content
			if content == "" {
				// Hash-skipped file: fetch content on demand for materialization.
				// Use rawAdapter + RemotePath when available (mounted paths),
				// otherwise use the regular adapter.
				catAdapter := adapter
				if rawAdapter != nil {
					catAdapter = rawAdapter
				}
				catRepo := change.Remote.RemoteRepo
				if catRepo == "" {
					catRepo = repo
				}
				cat, err := catAdapter.Cat(ctx, store.CatRequest{Repo: catRepo, Path: change.Remote.RemotePath})
				if err != nil {
					return remoteSyncResult{}, fmt.Errorf("cat %s for materialize: %w", change.Remote.RemotePath, err)
				}
				content = cat.Content
			}
			if err := writeMaterializedFile(change.Remote.LocalPath, content); err != nil {
				return remoteSyncResult{}, err
			}
			entries = append(entries, change.Entry)
			result.Materialized++
		default:
			return remoteSyncResult{}, fmt.Errorf("unknown sync action: %d", change.Action)
		}
	}

	manifest = syncmanifest.ReplaceUnder(manifest, root, entries)
	if err := syncmanifest.Save(manifestPath, manifest); err != nil {
		return remoteSyncResult{}, err
	}
	result.Manifest = manifest
	return result, nil
}

func (f remoteSyncFile) toManifestEntry(root string, materialized bool) syncmanifest.Entry {
	repo := f.RemoteRepo
	if repo == "" {
		repo = "self"
	}
	return syncmanifest.Entry{
		Local:        f.LocalPath,
		RemoteDoc:    "repo://" + repo + "/" + strings.Trim(f.RemotePath, "/"),
		Mount:        cleanSyncMount(root),
		ContentHash:  f.ContentHash,
		Size:         f.Size,
		MTime:        f.MTime,
		Materialized: materialized,
	}
}

func manifestEntriesByLocal(manifest syncmanifest.Manifest) map[string]syncmanifest.Entry {
	entries := make(map[string]syncmanifest.Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entries[entry.Local] = entry
	}
	return entries
}

func readLocalSyncFile(localPath string) (localSyncFile, bool, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return localSyncFile{}, false, nil
		}
		return localSyncFile{}, false, fmt.Errorf("stat %s: %w", localPath, err)
	}
	if info.IsDir() {
		return localSyncFile{}, false, fmt.Errorf("local path %s is a directory", localPath)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return localSyncFile{}, false, fmt.Errorf("read %s: %w", localPath, err)
	}
	content := string(data)
	return localSyncFile{
		LocalPath:   localPath,
		Content:     content,
		ContentHash: store.HashContent(content),
		Size:        info.Size(),
		MTime:       info.ModTime().UTC(),
	}, true, nil
}

func pushLocalConflict(ctx context.Context, adapter store.Adapter, repo string, local localSyncFile, remote remoteSyncFile, root string, resolver *mountadapter.Resolver) (syncmanifest.Entry, error) {
	resp, err := adapter.Put(ctx, store.PutRequest{
		Repo:    repo,
		Path:    remote.LocalPath,
		Content: local.Content,
	})
	if err != nil {
		return syncmanifest.Entry{}, err
	}
	return syncmanifest.Entry{
		Local:        local.LocalPath,
		RemoteDoc:    resolveRemoteDoc(resolver, repo, remote.LocalPath, resp.Node.Path),
		Mount:        cleanSyncMount(root),
		ContentHash:  local.ContentHash,
		Size:         local.Size,
		MTime:        local.MTime.Format(time.RFC3339),
		Materialized: true,
	}, nil
}

func writeMaterializedFile(localPath, content string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(localPath), err)
	}
	if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", localPath, err)
	}
	return nil
}

func defaultManifestPath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(".gxfs", "manifest.toml")
}

func loadManifest(path string) (syncmanifest.Manifest, error) {
	manifest := syncmanifest.Manifest{}
	if existing, err := syncmanifest.Load(path); err == nil {
		return existing, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return syncmanifest.Manifest{}, err
	}
	return manifest, nil
}

func refreshManifest(ctx context.Context, adapter, rawAdapter store.Adapter, repo, root, manifestPath string, materialize bool, resolver *mountadapter.Resolver) (remoteSyncResult, error) {
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return remoteSyncResult{}, err
	}

	plan, err := buildRemoteSyncPlanForRoot(ctx, adapter, rawAdapter, resolver, repo, root, manifest, remoteSyncOptions{Materialize: materialize})
	if err != nil {
		return remoteSyncResult{}, err
	}
	result, err := applyRemoteSyncPlan(ctx, adapter, rawAdapter, repo, root, manifestPath, manifest, plan, resolver)
	if err != nil {
		return remoteSyncResult{}, err
	}
	return result, nil
}

func newRefreshCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var manifestPath string
	cmd := &cobra.Command{
		Use:   "refresh <path>",
		Short: "Refresh the local GXFS manifest for a path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			manifestPath = defaultManifestPath(manifestPath)
			result, err := refreshManifest(cmd.Context(), adapter, rawAdapter, repo, root, manifestPath, false, resolver)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "refreshed %d file%s under %s\n", result.RemoteFiles, plural(result.RemoteFiles), root)
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	return cmd
}

func newMaterializeCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var manifestPath string
	cmd := &cobra.Command{
		Use:   "materialize <path>",
		Short: "Write GXFS docs under a path to local markdown files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			manifestPath = defaultManifestPath(manifestPath)
			result, err := refreshManifest(cmd.Context(), adapter, rawAdapter, repo, root, manifestPath, true, resolver)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "refreshed %d file%s under %s\n", result.RemoteFiles, plural(result.RemoteFiles), root)
			fmt.Fprintf(cmd.OutOrStdout(), "materialized %d file%s under %s\n", result.Materialized, plural(result.Materialized), root)
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	return cmd
}

func newDematerializeCommand() *cobra.Command {
	var manifestPath string
	var keepFiles bool
	cmd := &cobra.Command{
		Use:   "dematerialize <path>",
		Short: "Mark materialized GXFS docs as remote-only",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			manifestPath = defaultManifestPath(manifestPath)
			manifest, err := syncmanifest.Load(manifestPath)
			if err != nil {
				return err
			}
			entries := syncmanifest.EntriesUnder(manifest, root)
			if len(entries) == 0 {
				return fmt.Errorf("no manifest entries under %s", root)
			}

			dematerialized := 0
			for i := range entries {
				if entries[i].Materialized && !keepFiles {
					if err := removeMaterializedFile(entries[i].Local, root); err != nil {
						return err
					}
				}
				if entries[i].Materialized {
					dematerialized++
				}
				entries[i].Materialized = false
			}
			manifest = syncmanifest.UpdateEntries(manifest, entries)
			if err := syncmanifest.Save(manifestPath, manifest); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "dematerialized %d file%s under %s\n", dematerialized, plural(dematerialized), root)
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	cmd.Flags().BoolVar(&keepFiles, "keep-files", false, "update manifest without deleting local files")
	return cmd
}

func removeMaterializedFile(localPath, root string) error {
	if err := os.Remove(localPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", localPath, err)
	}
	if err := removeEmptyParents(localPath, root); err != nil {
		return err
	}
	return nil
}

func removeEmptyParents(localPath, root string) error {
	dir := filepath.Clean(filepath.Dir(localPath))
	stop := filepath.Clean(root)
	if stop == "." {
		return nil
	}

	for dir != "." && dir != string(filepath.Separator) {
		if dir == stop {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("read dir %s: %w", dir, err)
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("remove dir %s: %w", dir, err)
		}
		dir = filepath.Dir(dir)
	}
	return nil
}

func newMountCommand(adapter, rawAdapter store.Adapter, repo string) *cobra.Command {
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

			// Cross-repo mounts are always readonly.
			if targetRepo != repo && mode == "writable" {
				return fmt.Errorf("cross-repo mounts must be readonly")
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

	plan, err := buildRemoteSyncPlanFromFiles(remoteFiles, manifest, remoteSyncOptions{}, localPath)
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

func newInitCommand() *cobra.Command {
	var claude bool
	var agent string
	var noInstructions bool
	var repoName string
	var serverAddr string
	var docsPath string
	var authMode string

	repoName = "github.com/user/repo"
	serverAddr = "http://127.0.0.1:7635"
	docsPath = "docs"
	authMode = "bearer"

	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize .gxfs config in a repo",
		Long:  "Initialize .gxfs/settings.toml and .gxfs/mounts.toml in the target directory and inject GXFS usage instructions into AGENTS.md by default.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}

			if authMode != "bearer" && authMode != "none" {
				return fmt.Errorf("unsupported auth mode %q: use bearer or none", authMode)
			}
			docsPath = cleanLocalDocsPath(docsPath)
			if docsPath == "" {
				docsPath = "docs"
			}

			var target string
			if !noInstructions {
				agent = strings.ToLower(agent)
				if claude {
					if agent != "" && agent != "claude" {
						return fmt.Errorf("--claude cannot be combined with --agent %s", agent)
					}
					agent = "claude"
				}
				var err error
				target, err = instructionTargetPath(dir, agent)
				if err != nil {
					return err
				}
			}

			gxfsDir := filepath.Join(dir, ".gxfs")
			settingsPath := filepath.Join(gxfsDir, "settings.toml")
			mountsPath := filepath.Join(gxfsDir, "mounts.toml")
			templateData := initTemplateData{
				Repo:       repoName,
				ServerAddr: serverAddr,
				DocsPath:   docsPath,
				AuthMode:   authMode,
			}

			if _, err := os.Stat(settingsPath); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already exists, skipping\n", settingsPath)
			} else {
				if err := os.MkdirAll(gxfsDir, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", gxfsDir, err)
				}
				settingsContent, err := renderInitTemplate("settings", initSettingsTomlTemplate, templateData)
				if err != nil {
					return err
				}
				if err := os.WriteFile(settingsPath, []byte(settingsContent), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", settingsPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", settingsPath)
			}

			if _, err := os.Stat(mountsPath); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already exists, skipping\n", mountsPath)
			} else {
				if err := os.MkdirAll(gxfsDir, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", gxfsDir, err)
				}
				mountsContent, err := renderInitTemplate("mounts", initMountsTomlTemplate, templateData)
				if err != nil {
					return err
				}
				if err := os.WriteFile(mountsPath, []byte(mountsContent), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", mountsPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", mountsPath)
			}

			if !noInstructions {
				actual, err := upsertInstructions(target, docsPath)
				if err != nil {
					return err
				}
				if actual != target {
					fmt.Fprintf(cmd.OutOrStdout(), "updated GXFS instructions in %s (resolved to %s)\n", target, actual)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "updated GXFS instructions in %s\n", target)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "initialized %s\n", gxfsDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "agent instructions target: agents or claude")
	cmd.Flags().BoolVar(&claude, "claude", false, "append GXFS usage to CLAUDE.md (alias for --agent claude)")
	cmd.Flags().BoolVar(&noInstructions, "no-instructions", false, "only create .gxfs config, without writing agent instructions")
	cmd.Flags().StringVar(&repoName, "repo", repoName, "logical repository name")
	cmd.Flags().StringVar(&serverAddr, "server", serverAddr, "gxfs-server base URL")
	cmd.Flags().StringVar(&docsPath, "docs", docsPath, "local docs root")
	cmd.Flags().StringVar(&authMode, "auth", authMode, "auth mode: bearer or none")
	return cmd
}

type initTemplateData struct {
	Repo       string
	ServerAddr string
	DocsPath   string
	AuthMode   string
}

const initSettingsTomlTemplate = `version = 1
repo = "{{ .Repo }}"

[server]
addr = "{{ .ServerAddr }}"

[auth]
mode = "{{ .AuthMode }}"
token_env = "GXFS_TOKEN"

[docs]
path = "{{ .DocsPath }}"

[cache]
metadata_ttl = "5m"
content_ttl = "24h"
materialize = "explicit"
`

const initMountsTomlTemplate = `version = 1

[[mounts]]
local = "{{ .DocsPath }}"
remote = "repo://self/{{ .DocsPath }}"
mode = "writable"
source = "default"
`

const gxfsInstructionsStart = "<!-- GXFS_START -->"
const gxfsInstructionsEnd = "<!-- GXFS_END -->"

//go:embed instructions/agents.md
var gxfsInstructionsTemplate string

func instructionTargetPath(dir, agent string) (string, error) {
	switch strings.ToLower(agent) {
	case "", "agent", "agents":
		return filepath.Join(dir, "AGENTS.md"), nil
	case "claude":
		return filepath.Join(dir, "CLAUDE.md"), nil
	default:
		return "", fmt.Errorf("unsupported agent %q: supported agents are agents and claude", agent)
	}
}

func upsertInstructions(target, docsPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(target), err)
	}

	actual := target
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		actual = resolved
	}

	data, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", target, err)
	}

	block, err := renderGXFSInstructions(docsPath)
	if err != nil {
		return "", err
	}
	content := replaceMarkedBlock(string(data), block)
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", target, err)
	}
	return actual, nil
}

func replaceMarkedBlock(content, block string) string {
	start := strings.Index(content, gxfsInstructionsStart)
	end := strings.Index(content, gxfsInstructionsEnd)
	if start >= 0 && end >= start {
		end += len(gxfsInstructionsEnd)
		next := content[end:]
		if strings.HasPrefix(next, "\n") {
			next = next[1:]
		}
		content = content[:start] + strings.TrimSpace(block) + "\n" + next
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content
	}

	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if content != "" {
		content += "\n"
	}
	content += strings.TrimSpace(block) + "\n"
	return content
}

func renderGXFSInstructions(docsPath string) (string, error) {
	tmpl, err := template.New("gxfs-instructions").Option("missingkey=error").Parse(gxfsInstructionsTemplate)
	if err != nil {
		return "", fmt.Errorf("parse GXFS instructions template: %w", err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, struct {
		DocsPath string
	}{
		DocsPath: docsPath,
	}); err != nil {
		return "", fmt.Errorf("render GXFS instructions template: %w", err)
	}
	return out.String(), nil
}

func renderInitTemplate(name, raw string, data initTemplateData) (string, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render %s template: %w", name, err)
	}
	return out.String(), nil
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
			fmt.Fprintf(cmd.OutOrStdout(), "Repo: %s\n", repo)
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

// parseRepoRef parses a "repo://<name>/<path>" argument into repo and path.
// Repo names containing '/' must be URL-encoded (e.g. repo://github%2Fopenai-go/docs).
// Returns ("", "", false) if the argument is not a repo ref.
func parseRepoRef(arg string) (repo, p string, ok bool) {
	if !strings.HasPrefix(arg, "repo://") {
		return "", "", false
	}
	rest := strings.TrimPrefix(arg, "repo://")
	parts := strings.SplitN(rest, "/", 2)
	if parts[0] == "" {
		return "", "", false
	}
	decoded, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", false
	}
	repo = decoded
	if len(parts) == 2 && parts[1] != "" {
		p = "/" + parts[1]
	} else {
		p = "/"
	}
	return repo, p, true
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if isConfigFreeCommand(args) {
		cmd := newRootCommand(nil, nil, "", nil)
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
		path = ".gxfs/settings.toml"
	}
	cfg, err := config.LoadCLI(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	adapter, resolver, err := loadRuntimeAdapter(cfg, path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	rawAdapter := client.New(cfg.Server.Addr)
	cmd := newRootCommand(adapter, rawAdapter, cfg.Repo, resolver)
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

func isConfigFreeCommand(args []string) bool {
	if wantsHelp(args) {
		return true
	}
	return len(args) > 0 && args[0] == "init"
}

func loadRuntimeAdapter(cfg config.CLIConfig, settingsPath string) (store.Adapter, *mountadapter.Resolver, error) {
	mountsPath := filepath.Join(filepath.Dir(settingsPath), "mounts.toml")
	mountsCfg, err := config.LoadMounts(mountsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			mountsCfg = config.DefaultMounts(cfg)
		} else {
			return nil, nil, err
		}
	}

	resolver, err := mountadapter.NewResolver(cfg.Repo, mountsCfg.Mounts)
	if err != nil {
		return nil, nil, err
	}

	return mountadapter.NewAdapter(client.New(cfg.Server.Addr), resolver), resolver, nil
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "help" || arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func cleanLocalDocsPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return ""
	}
	return filepath.ToSlash(path.Clean(p))
}
