package memory

import (
	"context"
	"testing"

	"gxfs/internal/store"
	"gxfs/internal/vfs"
)

var _ store.Adapter = (*Adapter)(nil)

func newAdapter(t *testing.T) *Adapter {
	t.Helper()

	tree, err := vfs.New([]vfs.File{
		{Path: "/docs/readme.md", Content: "# Readme\nhello gxfs\n"},
		{Path: "/docs/main.go", Content: "package docs\n\ntype Adapter interface{}\n"},
	})
	if err != nil {
		t.Fatalf("vfs.New() error = %v", err)
	}
	return New(tree)
}

func TestAdapterDelegatesLS(t *testing.T) {
	adapter := newAdapter(t)

	resp, err := adapter.LS(context.Background(), store.LSRequest{Repo: "gxfs", Path: "/docs"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(resp.Nodes) != 2 || resp.Nodes[0].Name != "main.go" || resp.Nodes[1].Name != "readme.md" {
		t.Fatalf("LS() = %+v, want sorted docs files", resp.Nodes)
	}
}

func TestAdapterDelegatesCatGrepFindTreeAndStat(t *testing.T) {
	adapter := newAdapter(t)

	cat, err := adapter.Cat(context.Background(), store.CatRequest{Repo: "gxfs", Path: "/docs/readme.md"})
	if err != nil {
		t.Fatalf("Cat() error = %v", err)
	}
	if cat.Path != "/docs/readme.md" || cat.Content != "# Readme\nhello gxfs\n" {
		t.Fatalf("Cat() = %+v, want readme content", cat)
	}

	grep, err := adapter.Grep(context.Background(), store.GrepRequest{Repo: "gxfs", Path: "/", Pattern: "Adapter"})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	if len(grep.Matches) != 1 || grep.Matches[0].Path != "/docs/main.go" {
		t.Fatalf("Grep() = %+v, want Adapter match in main.go", grep.Matches)
	}

	find, err := adapter.Find(context.Background(), store.FindRequest{Repo: "gxfs", Path: "/", Name: "*.go"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(find.Nodes) != 1 || find.Nodes[0].Path != "/docs/main.go" {
		t.Fatalf("Find() = %+v, want main.go", find.Nodes)
	}

	tree, err := adapter.Tree(context.Background(), store.TreeRequest{Repo: "gxfs", Path: "/", Depth: 1})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if tree.Root.Path != "/" || tree.Text != "/\n  docs/\n" {
		t.Fatalf("Tree() = %+v, want root tree depth 1", tree)
	}

	stat, err := adapter.Stat(context.Background(), store.StatRequest{Repo: "gxfs", Path: "/docs"})
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if stat.Node.Path != "/docs" || stat.Node.Kind != vfs.KindDir {
		t.Fatalf("Stat() = %+v, want /docs dir", stat.Node)
	}
}
