package mount

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/austiecodes/gxfs/internal/config"
	"github.com/austiecodes/gxfs/internal/store"
)

type fakeStore struct {
	lsReqs       []store.LSRequest
	catReq       store.CatRequest
	putReq       store.PutRequest
	deleteReq    store.DeleteRequest
	editReq      store.EditRequest
	hashReq      store.HashRequest
	grepReq      []store.GrepRequest
	lsByPath     map[string][]store.Node
	findByPath   map[string][]store.Node
	grepByPath   map[string][]store.Match
	hashesByPath map[string][]store.ContentHash
}

type sourceRoutingFakeStore struct {
	*fakeStore
	sources map[string]store.Adapter
}

func (f *sourceRoutingFakeStore) AdapterForSource(_ context.Context, source store.SourceRef) (store.Adapter, error) {
	switch source.Kind {
	case store.SourceKindRepo:
		return f.fakeStore, nil
	case store.SourceKindDocs, store.SourceKindDocset:
		if adapter, ok := f.sources[string(source.Kind)+"://"+source.Name]; ok {
			return adapter, nil
		}
		return nil, fmt.Errorf("%w: %s", store.ErrUnknownSource, source.String())
	default:
		return nil, fmt.Errorf("%w: %s", store.ErrUnknownSource, source.String())
	}
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
	if f.findByPath != nil {
		return &store.FindResponse{Nodes: append([]store.Node(nil), f.findByPath[req.Path]...)}, nil
	}
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
	f.deleteReq = req
	return &store.DeleteResponse{}, nil
}

func (f *fakeStore) Edit(_ context.Context, req store.EditRequest) (*store.EditResponse, error) {
	f.editReq = req
	return &store.EditResponse{Path: req.Path, Replaced: 1, Content: "new"}, nil
}

func (f *fakeStore) BatchHashes(_ context.Context, req store.HashRequest) (*store.HashResponse, error) {
	f.hashReq = req
	if f.hashesByPath != nil {
		return &store.HashResponse{Hashes: append([]store.ContentHash(nil), f.hashesByPath[req.Path]...)}, nil
	}
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

func TestAdapterRoutesDocsSourceThroughSourceRouter(t *testing.T) {
	resolver, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/openai-go-sdk", Remote: "docs://openai-go-sdk/reference", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	docsStore := &fakeStore{
		lsByPath: map[string][]store.Node{
			"/reference": {
				{Path: "/reference/usage.md", Name: "usage.md", Kind: "file"},
			},
		},
		findByPath: map[string][]store.Node{
			"/reference": {
				{Path: "/reference/usage.md", Name: "usage.md", Kind: "file"},
			},
		},
		grepByPath: map[string][]store.Match{
			"/reference": {
				{Path: "/reference/usage.md", Line: 7, Text: "Responses API"},
			},
		},
		hashesByPath: map[string][]store.ContentHash{
			"/reference": {
				{Path: "/reference/usage.md", Hash: "sha256:usage"},
			},
		},
	}
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": &fakeStore{}},
		map[string]store.Adapter{"openai-go-sdk": docsStore},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}
	adapter := NewAdapter(registry, resolver)

	cat, err := adapter.Cat(context.Background(), store.CatRequest{Repo: "gxfs", Path: "docs/openai-go-sdk/usage.md"})
	if err != nil {
		t.Fatalf("Cat() error = %v", err)
	}
	if docsStore.catReq.Repo != "openai-go-sdk" || docsStore.catReq.Path != "/reference/usage.md" {
		t.Fatalf("docs cat req = %+v, want openai-go-sdk /reference/usage.md", docsStore.catReq)
	}
	if cat.Path != "/docs/openai-go-sdk/usage.md" {
		t.Fatalf("cat path = %q, want localized docs path", cat.Path)
	}

	ls, err := adapter.LS(context.Background(), store.LSRequest{Repo: "gxfs", Path: "docs/openai-go-sdk"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(docsStore.lsReqs) != 1 || docsStore.lsReqs[0].Repo != "openai-go-sdk" || docsStore.lsReqs[0].Path != "/reference" {
		t.Fatalf("docs ls reqs = %+v, want openai-go-sdk /reference", docsStore.lsReqs)
	}
	if len(ls.Nodes) != 1 || ls.Nodes[0].Path != "/docs/openai-go-sdk/usage.md" {
		t.Fatalf("ls nodes = %+v, want localized docs path", ls.Nodes)
	}

	found, err := adapter.Find(context.Background(), store.FindRequest{Repo: "gxfs", Path: "docs/openai-go-sdk", Name: "*.md"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(found.Nodes) != 1 || found.Nodes[0].Path != "/docs/openai-go-sdk/usage.md" {
		t.Fatalf("find nodes = %+v, want localized docs path", found.Nodes)
	}

	grep, err := adapter.Grep(context.Background(), store.GrepRequest{Repo: "gxfs", Path: "docs/openai-go-sdk", Pattern: "Responses"})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	if len(grep.Matches) != 1 || grep.Matches[0].Path != "/docs/openai-go-sdk/usage.md" {
		t.Fatalf("grep matches = %+v, want localized docs path", grep.Matches)
	}

	hashes, err := adapter.BatchHashes(context.Background(), store.HashRequest{Repo: "gxfs", Path: "docs/openai-go-sdk"})
	if err != nil {
		t.Fatalf("BatchHashes() error = %v", err)
	}
	if docsStore.hashReq.Repo != "openai-go-sdk" || docsStore.hashReq.Path != "/reference" {
		t.Fatalf("docs hash req = %+v, want openai-go-sdk /reference", docsStore.hashReq)
	}
	if len(hashes.Hashes) != 1 || hashes.Hashes[0].Path != "/docs/openai-go-sdk/usage.md" {
		t.Fatalf("hashes = %+v, want localized docs path", hashes.Hashes)
	}
}

func TestAdapterRoutesDocsetSourceThroughSourceRouterReadonly(t *testing.T) {
	resolver, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/best-practices", Remote: "docset://best-practices/go", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	docsetStore := &fakeStore{
		lsByPath: map[string][]store.Node{
			"/go": {
				{Path: "/go/errors.md", Name: "errors.md", Kind: "file"},
			},
		},
	}
	registry := &sourceRoutingFakeStore{
		fakeStore: &fakeStore{},
		sources: map[string]store.Adapter{
			"docset://best-practices": docsetStore,
		},
	}
	adapter := NewAdapter(registry, resolver)

	cat, err := adapter.Cat(context.Background(), store.CatRequest{Repo: "gxfs", Path: "docs/best-practices/errors.md"})
	if err != nil {
		t.Fatalf("Cat() error = %v", err)
	}
	if docsetStore.catReq.Repo != "best-practices" || docsetStore.catReq.Path != "/go/errors.md" {
		t.Fatalf("docset cat req = %+v, want best-practices /go/errors.md", docsetStore.catReq)
	}
	if cat.Path != "/docs/best-practices/errors.md" {
		t.Fatalf("cat path = %q, want localized docset path", cat.Path)
	}

	ls, err := adapter.LS(context.Background(), store.LSRequest{Repo: "gxfs", Path: "docs/best-practices"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(docsetStore.lsReqs) != 1 || docsetStore.lsReqs[0].Repo != "best-practices" || docsetStore.lsReqs[0].Path != "/go" {
		t.Fatalf("docset ls reqs = %+v, want best-practices /go", docsetStore.lsReqs)
	}
	if len(ls.Nodes) != 1 || ls.Nodes[0].Path != "/docs/best-practices/errors.md" {
		t.Fatalf("ls nodes = %+v, want localized docset path", ls.Nodes)
	}

	_, err = adapter.Put(context.Background(), store.PutRequest{Repo: "gxfs", Path: "docs/best-practices/new.md", Content: "x"})
	if !errors.Is(err, store.ErrReadOnlyMount) {
		t.Fatalf("Put() error = %v, want ErrReadOnlyMount", err)
	}
}

func TestAdapterWritesDocsSourceThroughSourceRouter(t *testing.T) {
	resolver, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/openai-go-sdk", Remote: "docs://openai-go-sdk/reference", Mode: "writable"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	docsStore := &fakeStore{}
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": &fakeStore{}},
		map[string]store.Adapter{"openai-go-sdk": docsStore},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}
	adapter := NewAdapter(registry, resolver)

	resp, err := adapter.Put(context.Background(), store.PutRequest{
		Repo:    "gxfs",
		Path:    "docs/openai-go-sdk/new.md",
		Content: "new content",
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if docsStore.putReq.Repo != "openai-go-sdk" || docsStore.putReq.Path != "/reference/new.md" || docsStore.putReq.Content != "new content" {
		t.Fatalf("docs put req = %+v, want routed writable docs request", docsStore.putReq)
	}
	if resp.Node.Path != "/docs/openai-go-sdk/new.md" {
		t.Fatalf("put response path = %q, want localized docs path", resp.Node.Path)
	}
}

func TestAdapterRejectsDocsSourceWithoutSourceRouter(t *testing.T) {
	resolver, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/openai-go-sdk", Remote: "docs://openai-go-sdk", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	adapter := NewAdapter(&fakeStore{}, resolver)

	_, err = adapter.Cat(context.Background(), store.CatRequest{Repo: "gxfs", Path: "docs/openai-go-sdk/usage.md"})
	if !errors.Is(err, store.ErrNotSupported) {
		t.Fatalf("Cat() error = %v, want ErrNotSupported", err)
	}
}
