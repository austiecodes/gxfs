package vfs

import (
	"reflect"
	"strings"
	"testing"
)

func newTestTree(t *testing.T) *Tree {
	t.Helper()

	tree, err := New([]File{
		{Path: "/docs/readme.md", Content: "# Readme\nhello gxfs\n"},
		{Path: "/docs/go/main.go", Content: "package main\n\ntype Adapter interface{}\n"},
		{Path: "/api.md", Content: "api docs\n"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return tree
}

func newLSOptionsTree(t *testing.T) *Tree {
	t.Helper()

	tree, err := New([]File{
		{Path: "/b.txt", Content: "bb", Size: 2, ModTime: "2026-01-01"},
		{Path: "/a.txt", Content: "aaa", Size: 3, ModTime: "2026-01-02"},
		{Path: "/c.txt", Content: "c", Size: 1, ModTime: "2026-01-03"},
		{Path: "/sub/d.txt", Content: "dddd", Size: 4, ModTime: "2026-01-04"},
		{Path: "/sub/e.txt", Content: "eeeee", Size: 5, ModTime: "2026-01-05"},
		{Path: "/.hidden", Content: "secret", Size: 6},
		{Path: "/sub/.secret", Content: "ssh", Size: 3},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return tree
}

func nodeNames(nodes []Node) []string {
	names := make([]string, len(nodes))
	for i, node := range nodes {
		names[i] = node.Name
	}
	return names
}

func TestTreeSynthesizesParentsAndListsSortedChildren(t *testing.T) {
	tree := newTestTree(t)

	root, err := tree.LS("/", LSOptions{})
	if err != nil {
		t.Fatalf("LS(/) error = %v", err)
	}
	if got, want := nodeNames(root), []string{"api.md", "docs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LS(/) names = %v, want %v", got, want)
	}
	if root[1].Kind != KindDir {
		t.Fatalf("docs kind = %q, want %q", root[1].Kind, KindDir)
	}

	docs, err := tree.LS("/docs", LSOptions{})
	if err != nil {
		t.Fatalf("LS(/docs) error = %v", err)
	}
	if got, want := nodeNames(docs), []string{"go", "readme.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LS(/docs) names = %v, want %v", got, want)
	}
}

func TestCatReturnsExactContent(t *testing.T) {
	tree := newTestTree(t)

	got, err := tree.Cat("/docs/readme.md")
	if err != nil {
		t.Fatalf("Cat() error = %v", err)
	}
	want := "# Readme\nhello gxfs\n"
	if got != want {
		t.Fatalf("Cat() = %q, want %q", got, want)
	}
}

func TestGrepReturnsPathLineAndText(t *testing.T) {
	tree := newTestTree(t)

	matches, err := tree.Grep("/", "Adapter", false, GrepOptions{})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("Grep() len = %d, want 1", len(matches))
	}
	match := matches[0]
	if match.Path != "/docs/go/main.go" || match.Line != 3 || match.Text != "type Adapter interface{}" {
		t.Fatalf("Grep() match = %+v, want Adapter line in main.go", match)
	}
}

func TestFindMatchesGlobUnderRoot(t *testing.T) {
	tree := newTestTree(t)

	nodes, err := tree.Find("/", "*.go", FindOptions{})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Path != "/docs/go/main.go" {
		t.Fatalf("Find() = %+v, want main.go", nodes)
	}
}

func TestTreeTextRespectsDepth(t *testing.T) {
	tree := newTestTree(t)

	got, err := tree.Tree("/", 1, TreeOptions{})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	want := "/\n  api.md\n  docs/\n"
	if got != want {
		t.Fatalf("Tree() = %q, want %q", got, want)
	}
}

func TestStatReturnsNode(t *testing.T) {
	tree := newTestTree(t)

	node, err := tree.Stat("/docs/go")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if node.Path != "/docs/go" || node.Kind != KindDir {
		t.Fatalf("Stat() = %+v, want /docs/go dir", node)
	}
}

func TestLSSortByName(t *testing.T) {
	tree := newLSOptionsTree(t)

	nodes, err := tree.LS("/", LSOptions{Sort: "name"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	got, want := nodeNames(nodes), []string{"a.txt", "b.txt", "c.txt", "sub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestLSSortBySize(t *testing.T) {
	tree := newLSOptionsTree(t)

	nodes, err := tree.LS("/", LSOptions{Sort: "size", All: true})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}

	got := nodeNames(nodes)
	want := []string{"sub", "c.txt", "b.txt", "a.txt", ".hidden"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestLSSortByMtime(t *testing.T) {
	tree := newLSOptionsTree(t)

	nodes, err := tree.LS("/", LSOptions{Sort: "mtime"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}

	got := nodeNames(nodes)
	want := []string{"sub", "b.txt", "a.txt", "c.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestLSReverse(t *testing.T) {
	tree := newLSOptionsTree(t)

	nodes, err := tree.LS("/", LSOptions{Sort: "name", Reverse: true})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}

	got := nodeNames(nodes)
	want := []string{"sub", "c.txt", "b.txt", "a.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestLSRecursive(t *testing.T) {
	tree := newLSOptionsTree(t)

	nodes, err := tree.LS("/", LSOptions{Recursive: true})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}

	got := nodeNames(nodes)
	want := []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "sub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestLSAllFiltersHidden(t *testing.T) {
	tree := newLSOptionsTree(t)

	t.Run("All=false excludes dot files", func(t *testing.T) {
		nodes, err := tree.LS("/", LSOptions{All: false})
		if err != nil {
			t.Fatalf("LS() error = %v", err)
		}
		for _, n := range nodes {
			if isHidden(n.Name) {
				t.Fatalf("found hidden file %q in results", n.Name)
			}
		}
	})

	t.Run("All=true includes dot files", func(t *testing.T) {
		nodes, err := tree.LS("/", LSOptions{All: true})
		if err != nil {
			t.Fatalf("LS() error = %v", err)
		}
		names := nodeNames(nodes)
		found := false
		for _, n := range names {
			if n == ".hidden" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected .hidden in %v", names)
		}
	})
}

func TestLSRecursiveSortAndHidden(t *testing.T) {
	tree := newLSOptionsTree(t)

	nodes, err := tree.LS("/", LSOptions{
		Recursive: true,
		Sort:      "size",
		Reverse:   true,
		All:       true,
	})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}

	got := nodeNames(nodes)
	want := []string{".hidden", "e.txt", "d.txt", "a.txt", ".secret", "b.txt", "c.txt", "sub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestLSDefaultZeroValueMatchesOriginal(t *testing.T) {
	tree := newLSOptionsTree(t)

	nodes, err := tree.LS("/", LSOptions{})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}

	got := nodeNames(nodes)
	want := []string{"a.txt", "b.txt", "c.txt", "sub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestLSRecursiveSkipsHiddenDirectories(t *testing.T) {
	tree, err := New([]File{
		{Path: "/normal.txt", Content: "normal"},
		{Path: "/.hidden_dir/secret.txt", Content: "secret"},
		{Path: "/.hidden_dir/deep/nested.txt", Content: "deep"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	t.Run("All=false skips hidden dir and all its descendants", func(t *testing.T) {
		nodes, err := tree.LS("/", LSOptions{Recursive: true, All: false})
		if err != nil {
			t.Fatalf("LS() error = %v", err)
		}
		got := nodeNames(nodes)
		want := []string{"normal.txt"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("names = %v, want %v", got, want)
		}
	})

	t.Run("All=true includes hidden dir and descendants", func(t *testing.T) {
		nodes, err := tree.LS("/", LSOptions{Recursive: true, All: true})
		if err != nil {
			t.Fatalf("LS() error = %v", err)
		}
		got := nodeNames(nodes)
		want := []string{".hidden_dir", "deep", "nested.txt", "normal.txt", "secret.txt"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("names = %v, want %v", got, want)
		}
	})
}

func TestLSEmptyDirectory(t *testing.T) {
	tree, err := New([]File{
		{Path: "/sub/orphan.txt", Content: "alone"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	nodes, err := tree.LS("/", LSOptions{})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "sub" {
		t.Fatalf("LS(/) = %v, want [sub]", nodeNames(nodes))
	}

	subNodes, err := tree.LS("/sub", LSOptions{})
	if err != nil {
		t.Fatalf("LS(/sub) error = %v", err)
	}
	if len(subNodes) != 1 {
		t.Fatalf("LS(/sub) = %d nodes, want 1", len(subNodes))
	}
}

func TestLSSortEqualElementsBySizeStable(t *testing.T) {
	tree, err := New([]File{
		{Path: "/a.txt", Content: "aa", Size: 10},
		{Path: "/b.txt", Content: "bb", Size: 10},
		{Path: "/c.txt", Content: "cc", Size: 10},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	forward, _ := tree.LS("/", LSOptions{Sort: "size", All: true})
	reverse, _ := tree.LS("/", LSOptions{Sort: "size", Reverse: true, All: true})

	gotFwd := nodeNames(forward)
	gotRev := nodeNames(reverse)
	want := []string{"a.txt", "b.txt", "c.txt"}
	if !reflect.DeepEqual(gotFwd, want) {
		t.Fatalf("forward = %v, want %v", gotFwd, want)
	}
	if !reflect.DeepEqual(gotRev, want) {
		t.Fatalf("reverse = %v, want %v", gotRev, want)
	}
}

// --- Grep extended tests ---

func newGrepTestTree(t *testing.T) *Tree {
	t.Helper()
	tree, err := New([]File{
		{Path: "/hello.txt", Content: "Hello World\nhello world\nHELLO WORLD\nGoodbye World"},
		{Path: "/code/main.go", Content: "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}"},
		{Path: "/code/util.go", Content: "package util\n\nfunc mainHelper() int {\n\treturn 42\n}"},
		{Path: "/docs/readme.md", Content: "# Readme\nThis is the main documentation.\nmain point here."},
		{Path: "/.hidden/secret.txt", Content: "secret main value\nhidden line"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return tree
}

func matchTexts(matches []Match) []string {
	texts := make([]string, len(matches))
	for i, m := range matches {
		texts[i] = m.Text
	}
	return texts
}

func matchPaths(matches []Match) []string {
	paths := make([]string, len(matches))
	for i, m := range matches {
		paths[i] = m.Path
	}
	return paths
}

func uniquePaths(matches []Match) []string {
	seen := make(map[string]bool)
	var result []string
	for _, m := range matches {
		if !seen[m.Path] {
			seen[m.Path] = true
			result = append(result, m.Path)
		}
	}
	return result
}

func TestGrepCaseInsensitive(t *testing.T) {
	tree := newGrepTestTree(t)

	tests := []struct {
		name    string
		pattern string
		regex   bool
		opts    GrepOptions
		want    []string
	}{
		{
			name:    "case sensitive matches exact case only",
			pattern: "hello",
			opts:    GrepOptions{},
			want: []string{
				`	fmt.Println("hello")`,
				"hello world",
			},
		},
		{
			name:    "case insensitive matches all cases",
			pattern: "hello",
			opts:    GrepOptions{CaseInsensitive: true},
			want: []string{
				`	fmt.Println("hello")`,
				"Hello World",
				"hello world",
				"HELLO WORLD",
			},
		},
		{
			name:    "case insensitive regex",
			pattern: "HELLO",
			regex:   true,
			opts:    GrepOptions{CaseInsensitive: true},
			want: []string{
				`	fmt.Println("hello")`,
				"Hello World",
				"hello world",
				"HELLO WORLD",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches, err := tree.Grep("/", tc.pattern, tc.regex, tc.opts)
			if err != nil {
				t.Fatalf("Grep() error = %v", err)
			}
			got := matchTexts(matches)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("texts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGrepInvert(t *testing.T) {
	tree, err := New([]File{
		{Path: "/dir/a.txt", Content: "Hello\nWorld\nFoo"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name    string
		pattern string
		regex   bool
		opts    GrepOptions
		want    []string
	}{
		{
			name:    "invert returns non-matching lines",
			pattern: "Hello",
			opts:    GrepOptions{Invert: true},
			want:    []string{"World", "Foo"},
		},
		{
			name:    "invert with regex",
			pattern: "Hello|Foo",
			regex:   true,
			opts:    GrepOptions{Invert: true},
			want:    []string{"World"},
		},
		{
			name:    "invert no match returns all lines",
			pattern: "missing",
			opts:    GrepOptions{Invert: true},
			want:    []string{"Hello", "World", "Foo"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches, err := tree.Grep("/dir", tc.pattern, tc.regex, tc.opts)
			if err != nil {
				t.Fatalf("Grep() error = %v", err)
			}
			got := matchTexts(matches)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("texts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGrepWholeWord(t *testing.T) {
	tree := newGrepTestTree(t)

	tests := []struct {
		name    string
		root    string
		pattern string
		regex   bool
		opts    GrepOptions
		want    []string
	}{
		{
			name:    "whole word matches standalone word in docs",
			root:    "/docs",
			pattern: "main",
			opts:    GrepOptions{WholeWord: true},
			want: []string{
				"This is the main documentation.",
				"main point here.",
			},
		},
		{
			name:    "whole word in code matches main but not mainHelper",
			root:    "/code",
			pattern: "main",
			opts:    GrepOptions{WholeWord: true},
			want:    []string{"package main", "func main() {"},
		},
		{
			name:    "whole word with regex uses word boundary",
			root:    "/code",
			pattern: "main",
			regex:   true,
			opts:    GrepOptions{WholeWord: true},
			want:    []string{"package main", "func main() {"},
		},
		{
			name:    "whole word skips partial then matches standalone",
			root:    "/docs",
			pattern: "ain",
			opts:    GrepOptions{WholeWord: true},
			want:    []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches, err := tree.Grep(tc.root, tc.pattern, tc.regex, tc.opts)
			if err != nil {
				t.Fatalf("Grep() error = %v", err)
			}
			got := matchTexts(matches)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("texts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGrepWholeLine(t *testing.T) {
	tree, err := New([]File{
		{Path: "/dir/a.txt", Content: "Hello World\nhello world\nGoodbye"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name    string
		pattern string
		regex   bool
		opts    GrepOptions
		want    []string
	}{
		{
			name:    "whole line exact match",
			pattern: "Hello World",
			opts:    GrepOptions{WholeLine: true},
			want:    []string{"Hello World"},
		},
		{
			name:    "whole line no partial match",
			pattern: "Hello",
			opts:    GrepOptions{WholeLine: true},
			want:    []string{},
		},
		{
			name:    "whole line with regex",
			pattern: "^Hello World$",
			regex:   true,
			opts:    GrepOptions{WholeLine: true},
			want:    []string{"Hello World"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches, err := tree.Grep("/dir", tc.pattern, tc.regex, tc.opts)
			if err != nil {
				t.Fatalf("Grep() error = %v", err)
			}
			got := matchTexts(matches)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("texts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGrepWholeWordAdvanceBug(t *testing.T) {
	// Tests that hasWholeWordSubstring correctly advances past a partial
	// match to find a later standalone word match.
	tree, err := New([]File{
		{Path: "/dir/a.txt", Content: "remain main"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	matches, err := tree.Grep("/dir", "main", false, GrepOptions{WholeWord: true})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1: %v", len(matches), matchTexts(matches))
	}
	if matches[0].Text != "remain main" {
		t.Fatalf("text = %q, want %q", matches[0].Text, "remain main")
	}
}

func TestGrepWholeLineEmptyMatch(t *testing.T) {
	// Verify nil vs empty slice for no matches
	tree, err := New([]File{
		{Path: "/dir/a.txt", Content: "Hello\nWorld"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	matches, err := tree.Grep("/dir", "Missing", false, GrepOptions{WholeLine: true})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	if matches != nil {
		t.Fatalf("expected nil matches, got %v", matches)
	}
}

func TestGrepContextLines(t *testing.T) {
	tree, err := New([]File{
		{Path: "/dir/a.txt", Content: "line1\nline2\nline3\nline4\nline5"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	t.Run("context after", func(t *testing.T) {
		matches, err := tree.Grep("/dir", "line2", false, GrepOptions{ContextAfter: 1})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("got %d matches, want 1", len(matches))
		}
		m := matches[0]
		if want := []string{"line3"}; !reflect.DeepEqual(m.After, want) {
			t.Fatalf("After = %v, want %v", m.After, want)
		}
		if m.Before != nil {
			t.Fatalf("Before = %v, want nil", m.Before)
		}
	})

	t.Run("context before", func(t *testing.T) {
		matches, err := tree.Grep("/dir", "line4", false, GrepOptions{ContextBefore: 2})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("got %d matches, want 1", len(matches))
		}
		m := matches[0]
		if want := []string{"line2", "line3"}; !reflect.DeepEqual(m.Before, want) {
			t.Fatalf("Before = %v, want %v", m.Before, want)
		}
		if m.After != nil {
			t.Fatalf("After = %v, want nil", m.After)
		}
	})

	t.Run("context both", func(t *testing.T) {
		matches, err := tree.Grep("/dir", "line3", false, GrepOptions{ContextBefore: 1, ContextAfter: 1})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("got %d matches, want 1", len(matches))
		}
		m := matches[0]
		if want := []string{"line2"}; !reflect.DeepEqual(m.Before, want) {
			t.Fatalf("Before = %v, want %v", m.Before, want)
		}
		if want := []string{"line4"}; !reflect.DeepEqual(m.After, want) {
			t.Fatalf("After = %v, want %v", m.After, want)
		}
	})

	t.Run("zero context omits fields", func(t *testing.T) {
		matches, err := tree.Grep("/dir", "line1", false, GrepOptions{})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("got %d matches, want 1", len(matches))
		}
		if matches[0].Before != nil || matches[0].After != nil {
			t.Fatalf("Before=%v After=%v, want nil/nil", matches[0].Before, matches[0].After)
		}
	})

	t.Run("context clamps at file boundaries", func(t *testing.T) {
		matches, err := tree.Grep("/dir", "line1", false, GrepOptions{ContextBefore: 5, ContextAfter: 5})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("got %d matches, want 1", len(matches))
		}
		m := matches[0]
		if len(m.Before) != 0 {
			t.Fatalf("Before = %v, want empty", m.Before)
		}
		if want := []string{"line2", "line3", "line4", "line5"}; !reflect.DeepEqual(m.After, want) {
			t.Fatalf("After = %v, want %v", m.After, want)
		}
	})
}

func TestGrepHiddenFileFiltering(t *testing.T) {
	tree := newGrepTestTree(t)

	t.Run("All=false skips hidden files", func(t *testing.T) {
		matches, err := tree.Grep("/", "main", false, GrepOptions{All: false})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		for _, m := range matches {
			if m.Path == "/.hidden/secret.txt" {
				t.Fatalf("found hidden file %q in results", m.Path)
			}
		}
	})

	t.Run("All=true includes hidden files", func(t *testing.T) {
		matches, err := tree.Grep("/", "secret", false, GrepOptions{All: true})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		paths := matchPaths(matches)
		found := false
		for _, p := range paths {
			if p == "/.hidden/secret.txt" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected /.hidden/secret.txt in %v", paths)
		}
	})
}

func TestGrepIncludeExclude(t *testing.T) {
	tree := newGrepTestTree(t)

	tests := []struct {
		name    string
		pattern string
		opts    GrepOptions
		want    []string
	}{
		{
			name:    "include only .go files",
			pattern: "main",
			opts:    GrepOptions{Include: "*.go"},
			want:    []string{"/code/main.go", "/code/util.go"},
		},
		{
			name:    "exclude .go files",
			pattern: "main",
			opts:    GrepOptions{Exclude: "*.go"},
			want:    []string{"/docs/readme.md"},
		},
		{
			name:    "include and exclude combined",
			pattern: "main",
			opts:    GrepOptions{Include: "*.go", Exclude: "util.go"},
			want:    []string{"/code/main.go"},
		},
		{
			name:    "include non-matching returns nothing",
			pattern: "main",
			opts:    GrepOptions{Include: "*.py"},
			want:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches, err := tree.Grep("/", tc.pattern, false, tc.opts)
			if err != nil {
				t.Fatalf("Grep() error = %v", err)
			}
			got := uniquePaths(matches)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGrepCombinations(t *testing.T) {
	tree := newGrepTestTree(t)

	t.Run("case insensitive + whole word", func(t *testing.T) {
		matches, err := tree.Grep("/docs", "MAIN", false, GrepOptions{
			CaseInsensitive: true,
			WholeWord:       true,
		})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		got := matchTexts(matches)
		want := []string{"This is the main documentation.", "main point here."}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("texts = %v, want %v", got, want)
		}
	})

	t.Run("invert + regex", func(t *testing.T) {
		singleTree, err := New([]File{
			{Path: "/dir/a.txt", Content: "Hello World\nhello world\nGoodbye"},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		matches, err := singleTree.Grep("/dir", "World", true, GrepOptions{
			Invert: true,
		})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		got := matchTexts(matches)
		// "World" matches "Hello World" only (case sensitive).
		// Inverted: "hello world" and "Goodbye" survive.
		want := []string{"hello world", "Goodbye"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("texts = %v, want %v", got, want)
		}
	})

	t.Run("context + whole word", func(t *testing.T) {
		matches, err := tree.Grep("/docs", "main", false, GrepOptions{
			WholeWord:     true,
			ContextBefore: 1,
			ContextAfter:  1,
		})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		if len(matches) != 2 {
			t.Fatalf("got %d matches, want 2: %v", len(matches), matchTexts(matches))
		}
		if want := []string{"# Readme"}; !reflect.DeepEqual(matches[0].Before, want) {
			t.Fatalf("match0 Before = %v, want %v", matches[0].Before, want)
		}
		if want := []string{"main point here."}; !reflect.DeepEqual(matches[0].After, want) {
			t.Fatalf("match0 After = %v, want %v", matches[0].After, want)
		}
	})

	t.Run("invert + case insensitive", func(t *testing.T) {
		singleTree, err := New([]File{
			{Path: "/dir/a.txt", Content: "Hello\nhello\nHELLO\nGoodbye"},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		matches, err := singleTree.Grep("/dir", "hello", false, GrepOptions{
			CaseInsensitive: true,
			Invert:          true,
		})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		got := matchTexts(matches)
		want := []string{"Goodbye"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("texts = %v, want %v", got, want)
		}
	})

	t.Run("all + include combined", func(t *testing.T) {
		matches, err := tree.Grep("/", "main", false, GrepOptions{
			All:     true,
			Include: "*.txt",
		})
		if err != nil {
			t.Fatalf("Grep() error = %v", err)
		}
		paths := uniquePaths(matches)
		if len(paths) != 1 || paths[0] != "/.hidden/secret.txt" {
			t.Fatalf("paths = %v, want [/.hidden/secret.txt]", paths)
		}
	})
}

func TestGrepZeroValueOptionsPreservesBehavior(t *testing.T) {
	tree := newGrepTestTree(t)

	matches, err := tree.Grep("/", "main", false, GrepOptions{})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected matches for 'main'")
	}
	for _, m := range matches {
		if m.Before != nil || m.After != nil {
			t.Fatalf("zero opts should not produce context: Before=%v After=%v", m.Before, m.After)
		}
	}
}

// --- Find extended tests ---

func newFindTestTree(t *testing.T) *Tree {
	t.Helper()
	tree, err := New([]File{
		{Path: "/a.txt", Content: "a", Size: 1},
		{Path: "/b.md", Content: "bb", Size: 2},
		{Path: "/sub/c.txt", Content: "ccc", Size: 3},
		{Path: "/sub/d.md", Content: "dddd", Size: 4},
		{Path: "/sub/deep/e.txt", Content: "eeeee", Size: 5},
		{Path: "/sub/deep/f.go", Content: "ffffff", Size: 6},
		{Path: "/.hidden/h.txt", Content: "hhh", Size: 3},
		{Path: "/.hidden/secret.md", Content: "ssss", Size: 4},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return tree
}

func TestFindTypeFilter(t *testing.T) {
	tree := newFindTestTree(t)

	tests := []struct {
		name string
		opts FindOptions
		want []string
	}{
		{
			name: "empty type returns files only (backward compat)",
			opts: FindOptions{},
			want: []string{"/a.txt", "/b.md", "/sub/c.txt", "/sub/d.md", "/sub/deep/e.txt", "/sub/deep/f.go"},
		},
		{
			name: "file type with All=true returns all files",
			opts: FindOptions{Type: "file", All: true},
			want: []string{"/.hidden/h.txt", "/.hidden/secret.md", "/a.txt", "/b.md", "/sub/c.txt", "/sub/d.md", "/sub/deep/e.txt", "/sub/deep/f.go"},
		},
		{
			name: "dir type returns non-hidden dirs",
			opts: FindOptions{Type: "dir"},
			want: []string{"/sub", "/sub/deep"},
		},
		{
			name: "dir type with All=true includes hidden dirs",
			opts: FindOptions{Type: "dir", All: true},
			want: []string{"/.hidden", "/sub", "/sub/deep"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes, err := tree.Find("/", "*", tc.opts)
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			got := nodePaths(nodes)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindMaxDepth(t *testing.T) {
	tree := newFindTestTree(t)

	tests := []struct {
		name string
		root string
		pat  string
		opts FindOptions
		want []string
	}{
		{
			name: "MaxDepth=1 from root returns files at depth 1 only",
			root: "/",
			pat:  "*",
			opts: FindOptions{MaxDepth: 1},
			want: []string{"/a.txt", "/b.md"},
		},
		{
			name: "MaxDepth=2 from root includes nested files",
			root: "/",
			pat:  "*",
			opts: FindOptions{MaxDepth: 2},
			want: []string{"/a.txt", "/b.md", "/sub/c.txt", "/sub/d.md"},
		},
		{
			name: "MaxDepth=1 from /sub returns only direct children",
			root: "/sub",
			pat:  "*",
			opts: FindOptions{MaxDepth: 1},
			want: []string{"/sub/c.txt", "/sub/d.md"},
		},
		{
			name: "MaxDepth=3 includes all non-hidden files",
			root: "/",
			pat:  "*",
			opts: FindOptions{MaxDepth: 3},
			want: []string{"/a.txt", "/b.md", "/sub/c.txt", "/sub/d.md", "/sub/deep/e.txt", "/sub/deep/f.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes, err := tree.Find(tc.root, tc.pat, tc.opts)
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			got := nodePaths(nodes)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindMinDepth(t *testing.T) {
	tree := newFindTestTree(t)

	tests := []struct {
		name string
		root string
		opts FindOptions
		want []string
	}{
		{
			name: "MinDepth=2 excludes shallow files",
			root: "/",
			opts: FindOptions{MinDepth: 2},
			want: []string{"/sub/c.txt", "/sub/d.md", "/sub/deep/e.txt", "/sub/deep/f.go"},
		},
		{
			name: "MinDepth=3 only deepest files",
			root: "/",
			opts: FindOptions{MinDepth: 3},
			want: []string{"/sub/deep/e.txt", "/sub/deep/f.go"},
		},
		{
			name: "MinDepth=2 from /sub",
			root: "/sub",
			opts: FindOptions{MinDepth: 2},
			want: []string{"/sub/deep/e.txt", "/sub/deep/f.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes, err := tree.Find(tc.root, "*", tc.opts)
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			got := nodePaths(nodes)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindHiddenFiltering(t *testing.T) {
	tree := newFindTestTree(t)

	t.Run("All=false excludes hidden", func(t *testing.T) {
		nodes, err := tree.Find("/", "*", FindOptions{})
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		for _, n := range nodes {
			if pathHasHiddenComponent(n.Path, "/") {
				t.Fatalf("found hidden node %q in results", n.Path)
			}
		}
	})

	t.Run("All=true includes hidden", func(t *testing.T) {
		nodes, err := tree.Find("/", "*", FindOptions{All: true})
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		paths := nodePaths(nodes)
		found := false
		for _, p := range paths {
			if p == "/.hidden/h.txt" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected /.hidden/h.txt in %v", paths)
		}
	})
}

func TestFindIName(t *testing.T) {
	tree := newFindTestTree(t)

	tests := []struct {
		name string
		pat  string
		opts FindOptions
		want []string
	}{
		{
			name: "case insensitive match *.TXT",
			pat:  "",
			opts: FindOptions{IName: "*.TXT"},
			want: []string{"/a.txt", "/sub/c.txt", "/sub/deep/e.txt"},
		},
		{
			name: "case sensitive *.TXT matches nothing",
			pat:  "*.TXT",
			opts: FindOptions{},
			want: nil,
		},
		{
			name: "iname with directory type",
			pat:  "",
			opts: FindOptions{IName: "SUB", Type: "dir"},
			want: []string{"/sub"},
		},
		{
			name: "iname hidden with All=true",
			pat:  "",
			opts: FindOptions{IName: "H*", All: true},
			want: []string{"/.hidden/h.txt"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes, err := tree.Find("/", tc.pat, tc.opts)
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			got := nodePaths(nodes)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindCombination(t *testing.T) {
	tree := newFindTestTree(t)

	t.Run("type=dir + MaxDepth=1", func(t *testing.T) {
		nodes, err := tree.Find("/", "*", FindOptions{Type: "dir", MaxDepth: 1})
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		got := nodePaths(nodes)
		want := []string{"/sub"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	})

	t.Run("type=file + MinDepth=2 + MaxDepth=2 + All=true", func(t *testing.T) {
		nodes, err := tree.Find("/", "*", FindOptions{Type: "file", MinDepth: 2, MaxDepth: 2, All: true})
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		got := nodePaths(nodes)
		want := []string{"/.hidden/h.txt", "/.hidden/secret.md", "/sub/c.txt", "/sub/d.md"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	})

	t.Run("IName + type=file + All=false", func(t *testing.T) {
		nodes, err := tree.Find("/", "", FindOptions{IName: "*.go", Type: "file"})
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		got := nodePaths(nodes)
		want := []string{"/sub/deep/f.go"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	})

	t.Run("type=dir + All=true + MaxDepth=2", func(t *testing.T) {
		nodes, err := tree.Find("/", "*", FindOptions{Type: "dir", All: true, MaxDepth: 2})
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		got := nodePaths(nodes)
		want := []string{"/.hidden", "/sub", "/sub/deep"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	})
}

func TestFindZeroValueOptionsPreservesBehavior(t *testing.T) {
	tree := newTestTree(t)

	nodes, err := tree.Find("/", "*.go", FindOptions{})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Path != "/docs/go/main.go" {
		t.Fatalf("Find() = %+v, want main.go only", nodes)
	}
}

// --- Tree extended tests ---

func newTreeOptionsTestTree(t *testing.T) *Tree {
	t.Helper()
	tree, err := New([]File{
		{Path: "/b.txt", Content: "bb", Size: 2, ModTime: "2026-01-01"},
		{Path: "/a.txt", Content: "aaa", Size: 3, ModTime: "2026-01-02"},
		{Path: "/sub/c.txt", Content: "cccc", Size: 4, ModTime: "2026-01-03"},
		{Path: "/sub/d.txt", Content: "ddddd", Size: 5, ModTime: "2026-01-04"},
		{Path: "/.hidden/e.txt", Content: "e", Size: 1},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return tree
}

func TestTreeAll(t *testing.T) {
	tree := newTreeOptionsTestTree(t)

	t.Run("All=false hides hidden", func(t *testing.T) {
		got, err := tree.Tree("/", -1, TreeOptions{})
		if err != nil {
			t.Fatalf("Tree() error = %v", err)
		}
		if strings.Contains(got, ".hidden") {
			t.Fatalf("hidden node in output: %q", got)
		}
	})

	t.Run("All=true shows hidden", func(t *testing.T) {
		got, err := tree.Tree("/", -1, TreeOptions{All: true})
		if err != nil {
			t.Fatalf("Tree() error = %v", err)
		}
		if !strings.Contains(got, ".hidden") {
			t.Fatalf("expected .hidden in output: %q", got)
		}
	})
}

func TestTreeDirsOnly(t *testing.T) {
	tree := newTreeOptionsTestTree(t)

	got, err := tree.Tree("/", -1, TreeOptions{DirsOnly: true, All: true})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if strings.Contains(got, "a.txt") || strings.Contains(got, "b.txt") || strings.Contains(got, "c.txt") {
		t.Fatalf("files in dirs-only output: %q", got)
	}
	if !strings.Contains(got, "sub/") {
		t.Fatalf("expected sub/ in output: %q", got)
	}
	if !strings.Contains(got, ".hidden/") {
		t.Fatalf("expected .hidden/ in output: %q", got)
	}
}

func TestTreeFullPath(t *testing.T) {
	tree := newTreeOptionsTestTree(t)

	got, err := tree.Tree("/", -1, TreeOptions{FullPath: true})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if !strings.Contains(got, "/a.txt") || !strings.Contains(got, "/sub/") {
		t.Fatalf("expected full paths in output: %q", got)
	}
	if strings.Contains(got, "  a.txt") {
		t.Fatalf("should not have bare name a.txt: %q", got)
	}
}

func TestTreeShowSize(t *testing.T) {
	tree := newTreeOptionsTestTree(t)

	got, err := tree.Tree("/", -1, TreeOptions{ShowSize: true})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if !strings.Contains(got, "a.txt [3]") {
		t.Fatalf("expected size annotation in output: %q", got)
	}
	if !strings.Contains(got, "b.txt [2]") {
		t.Fatalf("expected size annotation for b.txt: %q", got)
	}
	if strings.Contains(got, "sub/ [") {
		t.Fatalf("dir should not have size annotation: %q", got)
	}
}

func TestTreeSort(t *testing.T) {
	tree := newTreeOptionsTestTree(t)

	tests := []struct {
		name string
		opts TreeOptions
		want string
	}{
		{
			name: "sort by name (default)",
			opts: TreeOptions{},
			want: "/\n  a.txt\n  b.txt\n  sub/\n    c.txt\n    d.txt\n",
		},
		{
			name: "sort by size",
			opts: TreeOptions{Sort: "size"},
			want: "/\n  sub/\n    c.txt\n    d.txt\n  b.txt\n  a.txt\n",
		},
		{
			name: "sort by mtime",
			opts: TreeOptions{Sort: "mtime"},
			want: "/\n  sub/\n    c.txt\n    d.txt\n  b.txt\n  a.txt\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tree.Tree("/", 2, tc.opts)
			if err != nil {
				t.Fatalf("Tree() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Tree() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTreeDirsFirst(t *testing.T) {
	tree, err := New([]File{
		{Path: "/z.txt", Content: "z"},
		{Path: "/a_dir/nested.txt", Content: "n"},
		{Path: "/b.txt", Content: "b"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := tree.Tree("/", -1, TreeOptions{DirsFirst: true})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	want := "/\n  a_dir/\n    nested.txt\n  b.txt\n  z.txt\n"
	if got != want {
		t.Fatalf("Tree() = %q, want %q", got, want)
	}
}

func TestTreeZeroValueOptionsPreservesBehavior(t *testing.T) {
	tree := newTestTree(t)

	got, err := tree.Tree("/", 1, TreeOptions{})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	want := "/\n  api.md\n  docs/\n"
	if got != want {
		t.Fatalf("Tree() = %q, want %q", got, want)
	}
}

func nodePaths(nodes []Node) []string {
	if len(nodes) == 0 {
		return nil
	}
	paths := make([]string, len(nodes))
	for i, node := range nodes {
		paths[i] = node.Path
	}
	return paths
}
