package vfs

import (
	"reflect"
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

func nodeNames(nodes []Node) []string {
	names := make([]string, len(nodes))
	for i, node := range nodes {
		names[i] = node.Name
	}
	return names
}

func TestTreeSynthesizesParentsAndListsSortedChildren(t *testing.T) {
	tree := newTestTree(t)

	root, err := tree.LS("/")
	if err != nil {
		t.Fatalf("LS(/) error = %v", err)
	}
	if got, want := nodeNames(root), []string{"api.md", "docs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LS(/) names = %v, want %v", got, want)
	}
	if root[1].Kind != KindDir {
		t.Fatalf("docs kind = %q, want %q", root[1].Kind, KindDir)
	}

	docs, err := tree.LS("/docs")
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

	matches, err := tree.Grep("/", "Adapter", false)
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

	nodes, err := tree.Find("/", "*.go")
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Path != "/docs/go/main.go" {
		t.Fatalf("Find() = %+v, want main.go", nodes)
	}
}

func TestTreeTextRespectsDepth(t *testing.T) {
	tree := newTestTree(t)

	got, err := tree.Tree("/", 1)
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
