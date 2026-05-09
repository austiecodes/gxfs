package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

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
	cmd.AddCommand(newWriteCommand(adapter, repo))
	cmd.AddCommand(newEditCommand(adapter, repo))
	cmd.AddCommand(newDeleteCommand(adapter, repo))
	cmd.AddCommand(newInitCommand())
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

func newInitCommand() *cobra.Command {
	var claude bool
	var agent string
	var noInstructions bool

	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize .gxfs config in a repo",
		Long:  "Initialize .gxfs/settings.toml in the target directory and inject GXFS usage instructions into AGENTS.md by default.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
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

			if _, err := os.Stat(settingsPath); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already exists, skipping\n", settingsPath)
			} else {
				if err := os.MkdirAll(gxfsDir, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", gxfsDir, err)
				}
				if err := os.WriteFile(settingsPath, []byte(initSettingsToml), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", settingsPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", settingsPath)
			}

			if !noInstructions {
				actual, err := upsertInstructions(target, initDocsPath)
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
	return cmd
}

const initSettingsToml = `repo = "github.com/user/repo"

[server]
addr = "http://127.0.0.1:7635"

[mount]
include = ["/"]

[docs]
path = "/docs"
`

const initDocsPath = "/docs"

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
		path = ".gxfs/settings.toml"
	}
	cfg, err := config.LoadCLI(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	cmd := newRootCommand(client.New(cfg.Server.Addr), cfg.Repo)
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
		if arg == "help" || arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}
