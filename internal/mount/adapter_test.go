package mount

import (
	"context"
	"errors"
	"testing"

	"gxfs/internal/config"
	"gxfs/internal/store"
)

type fakeStore struct {
	lsReqs     []store.LSRequest
	catReq     store.CatRequest
	putReq     store.PutRequest
	grepReq    []store.GrepRequest
	lsByPath   map[string][]store.Node
	grepByPath map[string][]store.Match
}

func (f *fakeStore) LS(_ context.Context, req store.LSRequest) (*store.LSResponse, error) {
	f.lsReqs = append(f.lsReqs, req)
	return &store.LSResponse{Nodes: append([]store.Node(nil), f.lsByPath[req.Path]...)}, nil
}

func (f *fakeStore) Tree(_ context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	return &store.TreeResponse{
		Root: store.Node{Path: req.Path, Name: "remote-docs", Kind: "dir"},
		Text: "remote-docs/\n  guide.md\n",
	}, nil
}

func (f *fakeStore) Cat(_ context.Context, req store.CatRequest) (*store.CatResponse, error) {
	f.catReq = req
	return &store.CatResponse{Path: req.Path, Content: "content"}, nil
}

func (f *fakeStore) Grep(_ context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	f.grepReq = append(f.grepReq, req)
	return &store.GrepResponse{Matches: append([]store.Match(nil), f.grepByPath[req.Path]...)}, nil
}

func (f *fakeStore) Find(_ context.Context, req store.FindRequest) (*store.FindResponse, error) {
	return &store.FindResponse{Nodes: []store.Node{{Path: "/remote-docs/guide.md", Name: "guide.md", Kind: "file"}}}, nil
}

func (f *fakeStore) Stat(_ context.Context, req store.StatRequest) (*store.StatResponse, error) {
	return &store.StatResponse{Node: store.Node{Path: req.Path, Name: "guide.md", Kind: "file"}}, nil
}

func (f *fakeStore) Put(_ context.Context, req store.PutRequest) (*store.PutResponse, error) {
	f.putReq = req
	return &store.PutResponse{Node: store.Node{Path: req.Path, Name: "guide.md", Kind: "file"}}, nil
}

func (f *fakeStore) Delete(_ context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	return &store.DeleteResponse{}, nil
}

func (f *fakeStore) Edit(_ context.Context, req store.EditRequest) (*store.EditResponse, error) {
	return &store.EditResponse{Path: req.Path, Replaced: 1, Content: "new"}, nil
}

func (f *fakeStore) BatchHashes(_ context.Context, _ store.HashRequest) (*store.HashResponse, error) {
	return &store.HashResponse{Hashes: []store.ContentHash{}}, nil
}

func (f *fakeStore) Search(_ context.Context, _ store.SearchRequest) (*store.SearchResponse, error) {
	return &store.SearchResponse{Results: []store.SearchResult{}}, nil
}

func (f *fakeStore) Glob(_ context.Context, _ store.GlobRequest) (*store.GlobResponse, error) {
	return &store.GlobResponse{Results: []store.GlobResult{}}, nil
}

func (f *fakeStore) Locate(_ context.Context, _ store.LocateRequest) (*store.LocateResponse, error) {
	return &store.LocateResponse{Results: []store.LocateResult{}}, nil
}

func TestAdapterTranslatesRequestAndResponsePaths(t *testing.T) {
	resolver, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs", Remote: "repo://self/remote-docs", Mode: "writable"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	base := &fakeStore{
		lsByPath: map[string][]store.Node{
			"/remote-docs": {{Path: "/remote-docs/guide.md", Name: "guide.md", Kind: "file"}},
		},
	}
	adapter := NewAdapter(base, resolver)

	ls, err := adapter.LS(context.Background(), store.LSRequest{Repo: "gxfs", Path: "docs"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(base.lsReqs) != 1 || base.lsReqs[0].Path != "/remote-docs" {
		t.Fatalf("base ls path = %+v, want /remote-docs", base.lsReqs)
	}
	if ls.Nodes[0].Path != "/docs/guide.md" {
		t.Fatalf("response path = %q, want /docs/guide.md", ls.Nodes[0].Path)
	}

	cat, err := adapter.Cat(context.Background(), store.CatRequest{Repo: "gxfs", Path: "docs/guide.md"})
	if err != nil {
		t.Fatalf("Cat() error = %v", err)
	}
	if base.catReq.Path != "/remote-docs/guide.md" {
		t.Fatalf("base cat path = %q, want /remote-docs/guide.md", base.catReq.Path)
	}
	if cat.Path != "/docs/guide.md" {
		t.Fatalf("cat path = %q, want /docs/guide.md", cat.Path)
	}
}

func TestAdapterBuildsVirtualRootAndOverlaysNestedMounts(t *testing.T) {
	resolver, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs", Remote: "repo://self/remote-docs", Mode: "writable"},
		{Local: "docs/shared", Remote: "repo://self/shared-docs", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	base := &fakeStore{
		lsByPath: map[string][]store.Node{
			"/remote-docs": {
				{Path: "/remote-docs/guide.md", Name: "guide.md", Kind: "file"},
				{Path: "/remote-docs/shared", Name: "shared", Kind: "dir"},
				{Path: "/remote-docs/shared/old.md", Name: "old.md", Kind: "file"},
			},
			"/shared-docs": {
				{Path: "/shared-docs/new.md", Name: "new.md", Kind: "file"},
			},
		},
		grepByPath: map[string][]store.Match{
			"/remote-docs": {
				{Path: "/remote-docs/guide.md", Line: 1, Text: "Adapter"},
				{Path: "/remote-docs/shared/old.md", Line: 2, Text: "Adapter"},
			},
			"/shared-docs": {
				{Path: "/shared-docs/new.md", Line: 3, Text: "Adapter"},
			},
		},
	}
	adapter := NewAdapter(base, resolver)

	root, err := adapter.LS(context.Background(), store.LSRequest{Repo: "gxfs", Path: "/"})
	if err != nil {
		t.Fatalf("LS(/) error = %v", err)
	}
	if len(root.Nodes) != 1 || root.Nodes[0].Path != "/docs" || root.Nodes[0].Kind != "dir" {
		t.Fatalf("root nodes = %+v, want /docs dir", root.Nodes)
	}

	found, err := adapter.Find(context.Background(), store.FindRequest{Repo: "gxfs", Path: "docs", Name: "*.md"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(found.Nodes) != 2 {
		t.Fatalf("find node count = %d, want 2; nodes = %+v", len(found.Nodes), found.Nodes)
	}
	if found.Nodes[0].Path != "/docs/guide.md" || found.Nodes[1].Path != "/docs/shared/new.md" {
		t.Fatalf("find nodes = %+v, want overlay-localized paths", found.Nodes)
	}

	grep, err := adapter.Grep(context.Background(), store.GrepRequest{Repo: "gxfs", Path: "/", Pattern: "Adapter"})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	if len(grep.Matches) != 2 {
		t.Fatalf("grep match count = %d, want 2; matches = %+v", len(grep.Matches), grep.Matches)
	}
	if grep.Matches[0].Path != "/docs/guide.md" || grep.Matches[1].Path != "/docs/shared/new.md" {
		t.Fatalf("grep matches = %+v, want overlaid matches", grep.Matches)
	}
}

func TestAdapterRejectsReadOnlyWrites(t *testing.T) {
	resolver, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/shared", Remote: "repo://self/shared", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	adapter := NewAdapter(&fakeStore{}, resolver)

	_, err = adapter.Put(context.Background(), store.PutRequest{Repo: "gxfs", Path: "docs/shared/a.md", Content: "x"})
	if !errors.Is(err, store.ErrReadOnlyMount) {
		t.Fatalf("Put() error = %v, want ErrReadOnlyMount", err)
	}
}
