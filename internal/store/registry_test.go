package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/austiecodes/gxfs/internal/store"
)

type registryFakeAdapter struct {
	lsReq       store.LSRequest
	invalidated bool
}

func (f *registryFakeAdapter) LS(_ context.Context, req store.LSRequest) (*store.LSResponse, error) {
	f.lsReq = req
	return &store.LSResponse{Nodes: []store.Node{{Path: req.Path, Name: "docs", Kind: "dir"}}}, nil
}

func (f *registryFakeAdapter) Tree(context.Context, store.TreeRequest) (*store.TreeResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Cat(context.Context, store.CatRequest) (*store.CatResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Grep(context.Context, store.GrepRequest) (*store.GrepResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Find(context.Context, store.FindRequest) (*store.FindResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Stat(context.Context, store.StatRequest) (*store.StatResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Put(context.Context, store.PutRequest) (*store.PutResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Delete(context.Context, store.DeleteRequest) (*store.DeleteResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Edit(context.Context, store.EditRequest) (*store.EditResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Invalidate() {
	f.invalidated = true
}

func (f *registryFakeAdapter) BatchHashes(_ context.Context, _ store.HashRequest) (*store.HashResponse, error) {
	return &store.HashResponse{Hashes: []store.ContentHash{}}, nil
}

func (f *registryFakeAdapter) Search(_ context.Context, _ store.SearchRequest) (*store.SearchResponse, error) {
	return &store.SearchResponse{Results: []store.SearchResult{}}, nil
}

func (f *registryFakeAdapter) Glob(_ context.Context, _ store.GlobRequest) (*store.GlobResponse, error) {
	return &store.GlobResponse{Results: []store.GlobResult{}}, nil
}

func (f *registryFakeAdapter) Locate(_ context.Context, _ store.LocateRequest) (*store.LocateResponse, error) {
	return &store.LocateResponse{Results: []store.LocateResult{}}, nil
}

func TestRegistryRoutesByRepo(t *testing.T) {
	alpha := &registryFakeAdapter{}
	beta := &registryFakeAdapter{}
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"alpha": alpha,
		"beta":  beta,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	resp, err := registry.LS(context.Background(), store.LSRequest{Repo: "beta", Path: "/docs"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(resp.Nodes) != 1 || beta.lsReq.Repo != "beta" || beta.lsReq.Path != "/docs" {
		t.Fatalf("beta LS req = %+v, resp = %+v", beta.lsReq, resp)
	}
	if alpha.lsReq.Repo != "" {
		t.Fatalf("alpha received request: %+v", alpha.lsReq)
	}
}

func TestRegistryRejectsUnknownRepo(t *testing.T) {
	registry, err := store.NewRegistry(map[string]store.Adapter{"alpha": &registryFakeAdapter{}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	_, err = registry.LS(context.Background(), store.LSRequest{Repo: "missing", Path: "/"})
	if !errors.Is(err, store.ErrUnknownRepo) {
		t.Fatalf("LS() error = %v, want ErrUnknownRepo", err)
	}
}

func TestRegistryInvalidatesAllAdapters(t *testing.T) {
	alpha := &registryFakeAdapter{}
	beta := &registryFakeAdapter{}
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"alpha": alpha,
		"beta":  beta,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	registry.Invalidate()

	if !alpha.invalidated || !beta.invalidated {
		t.Fatalf("invalidated = %v/%v, want both true", alpha.invalidated, beta.invalidated)
	}
}

func TestNamespaceRegistryReposExcludesDocsNamespaces(t *testing.T) {
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{
			"alpha": &registryFakeAdapter{},
			"beta":  &registryFakeAdapter{},
		},
		map[string]store.Adapter{
			"openai-go-sdk": &registryFakeAdapter{},
		},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}

	repos := registry.Repos()
	if len(repos) != 2 || repos[0] != "alpha" || repos[1] != "beta" {
		t.Fatalf("Repos() = %+v, want only sorted repos", repos)
	}
}

func TestRegistryMountSourcesListsRepos(t *testing.T) {
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"gxfs":             &registryFakeAdapter{},
		"github/openai-go": &registryFakeAdapter{},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	sources, err := registry.MountSources(context.Background())
	if err != nil {
		t.Fatalf("MountSources() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("MountSources() len = %d, want 2: %+v", len(sources), sources)
	}
	if sources[0].Ref != "repo://github%2Fopenai-go" || sources[0].Kind != store.SourceKindRepo || sources[0].Name != "github/openai-go" {
		t.Fatalf("source[0] = %+v, want escaped github/openai-go repo", sources[0])
	}
	if sources[1].Ref != "repo://gxfs" || sources[1].Kind != store.SourceKindRepo || sources[1].Name != "gxfs" {
		t.Fatalf("source[1] = %+v, want gxfs repo", sources[1])
	}
}

func TestNamespaceRegistryMountSourcesListsReposAndDocs(t *testing.T) {
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{
			"gxfs":             &registryFakeAdapter{},
			"github/openai-go": &registryFakeAdapter{},
		},
		map[string]store.Adapter{
			"openai-go-sdk": &registryFakeAdapter{},
			"team/playbook": &registryFakeAdapter{},
		},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}

	sources, err := registry.MountSources(context.Background())
	if err != nil {
		t.Fatalf("MountSources() error = %v", err)
	}
	want := []store.MountSource{
		{Ref: "docs://openai-go-sdk", Kind: store.SourceKindDocs, Name: "openai-go-sdk", Description: "shared docs namespace"},
		{Ref: "docs://team%2Fplaybook", Kind: store.SourceKindDocs, Name: "team/playbook", Description: "shared docs namespace"},
		{Ref: "repo://github%2Fopenai-go", Kind: store.SourceKindRepo, Name: "github/openai-go", Description: "repository namespace"},
		{Ref: "repo://gxfs", Kind: store.SourceKindRepo, Name: "gxfs", Description: "repository namespace"},
	}
	if len(sources) != len(want) {
		t.Fatalf("MountSources() len = %d, want %d: %+v", len(sources), len(want), sources)
	}
	for i := range want {
		if sources[i].Ref != want[i].Ref ||
			sources[i].Kind != want[i].Kind ||
			sources[i].Name != want[i].Name ||
			sources[i].Description != want[i].Description {
			t.Fatalf("source[%d] = %+v, want %+v", i, sources[i], want[i])
		}
	}
}

func TestNamespaceRegistryAdapterForSource(t *testing.T) {
	repoAdapter := &registryFakeAdapter{}
	docsAdapter := &registryFakeAdapter{}
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": repoAdapter},
		map[string]store.Adapter{"openai-go-sdk": docsAdapter},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}

	gotRepo, err := registry.AdapterForSource(context.Background(), store.SourceRef{Kind: store.SourceKindRepo, Name: "gxfs"})
	if err != nil {
		t.Fatalf("AdapterForSource(repo) error = %v", err)
	}
	if gotRepo != repoAdapter {
		t.Fatalf("AdapterForSource(repo) = %p, want %p", gotRepo, repoAdapter)
	}

	gotDocs, err := registry.AdapterForSource(context.Background(), store.SourceRef{Kind: store.SourceKindDocs, Name: "openai-go-sdk"})
	if err != nil {
		t.Fatalf("AdapterForSource(docs) error = %v", err)
	}
	if gotDocs != docsAdapter {
		t.Fatalf("AdapterForSource(docs) = %p, want %p", gotDocs, docsAdapter)
	}

	if _, err := registry.AdapterForSource(context.Background(), store.SourceRef{Kind: store.SourceKindRepo, Name: "missing"}); !errors.Is(err, store.ErrUnknownRepo) {
		t.Fatalf("AdapterForSource(unknown repo) error = %v, want ErrUnknownRepo", err)
	}
	if _, err := registry.AdapterForSource(context.Background(), store.SourceRef{Kind: store.SourceKindDocs, Name: "missing"}); !errors.Is(err, store.ErrUnknownSource) {
		t.Fatalf("AdapterForSource(unknown docs) error = %v, want ErrUnknownSource", err)
	}
	if _, err := registry.AdapterForSource(context.Background(), store.SourceRef{Kind: store.SourceKindDocset, Name: "best"}); !errors.Is(err, store.ErrNotSupported) {
		t.Fatalf("AdapterForSource(docset) error = %v, want ErrNotSupported", err)
	}
}

func TestNamespaceRegistryRejectsInvalidDocsAdapter(t *testing.T) {
	_, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": &registryFakeAdapter{}},
		map[string]store.Adapter{"": &registryFakeAdapter{}},
	)
	if err == nil {
		t.Fatal("NewNamespaceRegistry() error = nil, want empty docs namespace error")
	}

	_, err = store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": &registryFakeAdapter{}},
		map[string]store.Adapter{"openai-go-sdk": nil},
	)
	if err == nil {
		t.Fatal("NewNamespaceRegistry() error = nil, want nil docs adapter error")
	}
}

func TestNamespaceRegistryInvalidatesDocsAdapters(t *testing.T) {
	repoAdapter := &registryFakeAdapter{}
	docsAdapter := &registryFakeAdapter{}
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": repoAdapter},
		map[string]store.Adapter{"openai-go-sdk": docsAdapter},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}

	registry.Invalidate()

	if !repoAdapter.invalidated || !docsAdapter.invalidated {
		t.Fatalf("invalidated repo/docs = %v/%v, want both true", repoAdapter.invalidated, docsAdapter.invalidated)
	}
}
