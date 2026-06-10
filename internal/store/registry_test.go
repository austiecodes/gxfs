package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/austiecodes/rolio/internal/store"
)

type registryFakeAdapter struct {
	lsReq       store.LSRequest
	treeReq     store.TreeRequest
	catReq      store.CatRequest
	statReq     store.StatRequest
	invalidated bool
	lsByPath    map[string][]store.Node
	treeByPath  map[string]*store.TreeResponse
	catByPath   map[string]*store.CatResponse
}

func (f *registryFakeAdapter) LS(_ context.Context, req store.LSRequest) (*store.LSResponse, error) {
	f.lsReq = req
	nodes, ok := f.lsByPath[req.Path]
	if !ok {
		nodes = []store.Node{{Path: req.Path, Name: "docs", Kind: "dir"}}
	}
	return &store.LSResponse{Nodes: append([]store.Node(nil), nodes...), Total: len(nodes)}, nil
}

func (f *registryFakeAdapter) Tree(_ context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	f.treeReq = req
	if resp, ok := f.treeByPath[req.Path]; ok {
		cp := *resp
		return &cp, nil
	}
	return &store.TreeResponse{
		Root: store.Node{Path: req.Path, Name: "docs", Kind: "dir"},
		Text: req.Path + "\n",
	}, nil
}

func (f *registryFakeAdapter) Cat(_ context.Context, req store.CatRequest) (*store.CatResponse, error) {
	f.catReq = req
	if resp, ok := f.catByPath[req.Path]; ok {
		cp := *resp
		return &cp, nil
	}
	return &store.CatResponse{Path: req.Path, Content: "content"}, nil
}

func (f *registryFakeAdapter) Grep(context.Context, store.GrepRequest) (*store.GrepResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Find(context.Context, store.FindRequest) (*store.FindResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Stat(_ context.Context, req store.StatRequest) (*store.StatResponse, error) {
	f.statReq = req
	return &store.StatResponse{Node: store.Node{Path: req.Path, Name: "node", Kind: "dir"}}, nil
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

func TestRegistryRootLSReturnsSourceCategories(t *testing.T) {
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"austiecodes/xxxx": &registryFakeAdapter{}},
		map[string]store.Adapter{"openai-go-sdk": &registryFakeAdapter{}},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}

	resp, err := registry.LS(context.Background(), store.LSRequest{Path: "/"})
	if err != nil {
		t.Fatalf("LS(/) error = %v", err)
	}

	assertNodePaths(t, resp.Nodes, []string{"/docsets", "/repos"})
	assertAllDirs(t, resp.Nodes)
}

func TestRegistryReposLSReturnsOwnerDirectories(t *testing.T) {
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"austiecodes/xxxx": &registryFakeAdapter{},
		"openai/rolio":       &registryFakeAdapter{},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	resp, err := registry.LS(context.Background(), store.LSRequest{Path: "/repos"})
	if err != nil {
		t.Fatalf("LS(/repos) error = %v", err)
	}

	assertNodePaths(t, resp.Nodes, []string{"/repos/austiecodes", "/repos/openai"})
	assertAllDirs(t, resp.Nodes)
}

func TestRegistryStatRepoSourceRootIsVirtualDir(t *testing.T) {
	adapter := &registryFakeAdapter{}
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"austiecodes/xxxx": adapter,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	resp, err := registry.Stat(context.Background(), store.StatRequest{Path: "/repos/austiecodes/xxxx"})
	if err != nil {
		t.Fatalf("Stat(/repos/austiecodes/xxxx) error = %v", err)
	}
	if resp.Node.Path != "/repos/austiecodes/xxxx" || resp.Node.Name != "xxxx" || resp.Node.Kind != "dir" {
		t.Fatalf("Stat() node = %+v, want repo source dir", resp.Node)
	}
	if adapter.statReq != (store.StatRequest{}) {
		t.Fatalf("underlying adapter received stat = %+v, want none", adapter.statReq)
	}
}

func TestRegistryRoutesReposByLongestPrefix(t *testing.T) {
	short := &registryFakeAdapter{}
	xxxx := &registryFakeAdapter{
		lsByPath: map[string][]store.Node{
			"/docs": {
				{Path: "/docs/guide.md", Name: "guide.md", Kind: "file"},
			},
		},
	}
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"austiecodes":       short,
		"austiecodes/xxxx": xxxx,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	resp, err := registry.LS(context.Background(), store.LSRequest{Path: "/repos/austiecodes/xxxx/docs"})
	if err != nil {
		t.Fatalf("LS(/repos/austiecodes/xxxx/docs) error = %v", err)
	}
	if xxxx.lsReq.Repo != "austiecodes/xxxx" || xxxx.lsReq.Path != "/docs" {
		t.Fatalf("xxxx LS req = %+v, want austiecodes/xxxx /docs", xxxx.lsReq)
	}
	if short.lsReq != (store.LSRequest{}) {
		t.Fatalf("short repo received LS req = %+v, want none", short.lsReq)
	}
	assertNodePaths(t, resp.Nodes, []string{"/repos/austiecodes/xxxx/docs/guide.md"})
}

func TestRegistryCatRoutesReposByLongestPrefix(t *testing.T) {
	xxxx := &registryFakeAdapter{
		catByPath: map[string]*store.CatResponse{
			"/docs/guide.md": {Path: "/docs/guide.md", Content: "guide"},
		},
	}
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"austiecodes/xxxx": xxxx,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	resp, err := registry.Cat(context.Background(), store.CatRequest{Path: "/repos/austiecodes/xxxx/docs/guide.md"})
	if err != nil {
		t.Fatalf("Cat(/repos/austiecodes/xxxx/docs/guide.md) error = %v", err)
	}
	if xxxx.catReq.Repo != "austiecodes/xxxx" || xxxx.catReq.Path != "/docs/guide.md" {
		t.Fatalf("cat req = %+v, want austiecodes/xxxx /docs/guide.md", xxxx.catReq)
	}
	if resp.Path != "/repos/austiecodes/xxxx/docs/guide.md" || resp.Content != "guide" {
		t.Fatalf("cat resp = %+v, want localized path/content", resp)
	}
}

func TestRegistryTreeFullPathLocalizesRepoDescendants(t *testing.T) {
	xxxx := &registryFakeAdapter{
		treeByPath: map[string]*store.TreeResponse{
			"/docs": {
				Root: store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
				Text: "/docs\n" +
					"  /docs/guide.md\n" +
					"  /docs/nested/\n" +
					"    /docs/nested/ref.md\n",
			},
		},
	}
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"austiecodes/xxxx": xxxx,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	resp, err := registry.Tree(context.Background(), store.TreeRequest{
		Path:     "/repos/austiecodes/xxxx/docs",
		FullPath: true,
	})
	if err != nil {
		t.Fatalf("Tree(/repos/austiecodes/xxxx/docs) error = %v", err)
	}
	if xxxx.treeReq.Repo != "austiecodes/xxxx" || xxxx.treeReq.Path != "/docs" || !xxxx.treeReq.FullPath {
		t.Fatalf("tree req = %+v, want austiecodes/xxxx /docs full path", xxxx.treeReq)
	}
	want := "/repos/austiecodes/xxxx/docs\n" +
		"  /repos/austiecodes/xxxx/docs/guide.md\n" +
		"  /repos/austiecodes/xxxx/docs/nested/\n" +
		"    /repos/austiecodes/xxxx/docs/nested/ref.md\n"
	if resp.Text != want {
		t.Fatalf("tree text = %q, want %q", resp.Text, want)
	}
}

func TestRegistryRoutesDocsetsByLongestPrefix(t *testing.T) {
	docset := &registryFakeAdapter{
		catByPath: map[string]*store.CatResponse{
			"/reference/usage.md": {Path: "/reference/usage.md", Content: "usage"},
		},
	}
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"rolio": &registryFakeAdapter{}},
		map[string]store.Adapter{
			"team":          &registryFakeAdapter{},
			"team/playbook": docset,
		},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}

	resp, err := registry.Cat(context.Background(), store.CatRequest{Path: "/docsets/team/playbook/reference/usage.md"})
	if err != nil {
		t.Fatalf("Cat(/docsets/team/playbook/reference/usage.md) error = %v", err)
	}
	if docset.catReq.Repo != "team/playbook" || docset.catReq.Path != "/reference/usage.md" {
		t.Fatalf("docset cat req = %+v, want team/playbook /reference/usage.md", docset.catReq)
	}
	if resp.Path != "/docsets/team/playbook/reference/usage.md" {
		t.Fatalf("cat path = %q, want localized docset path", resp.Path)
	}
}

func TestRegistryTreeFullPathLocalizesDocsetDescendants(t *testing.T) {
	docset := &registryFakeAdapter{
		treeByPath: map[string]*store.TreeResponse{
			"/reference": {
				Root: store.Node{Path: "/reference", Name: "reference", Kind: "dir"},
				Text: "/reference\n" +
					"  /reference/usage.md\n" +
					"  /reference/deep/\n" +
					"    /reference/deep/topic.md [12]\n",
			},
		},
	}
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"rolio": &registryFakeAdapter{}},
		map[string]store.Adapter{"team/playbook": docset},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}

	resp, err := registry.Tree(context.Background(), store.TreeRequest{
		Path:     "/docsets/team/playbook/reference",
		FullPath: true,
		ShowSize: true,
	})
	if err != nil {
		t.Fatalf("Tree(/docsets/team/playbook/reference) error = %v", err)
	}
	if docset.treeReq.Repo != "team/playbook" || docset.treeReq.Path != "/reference" || !docset.treeReq.FullPath {
		t.Fatalf("docset tree req = %+v, want team/playbook /reference full path", docset.treeReq)
	}
	want := "/docsets/team/playbook/reference\n" +
		"  /docsets/team/playbook/reference/usage.md\n" +
		"  /docsets/team/playbook/reference/deep/\n" +
		"    /docsets/team/playbook/reference/deep/topic.md [12]\n"
	if resp.Text != want {
		t.Fatalf("tree text = %q, want %q", resp.Text, want)
	}
}

func TestRegistryUnknownRootPathReturnsNotFound(t *testing.T) {
	registry, err := store.NewRegistry(map[string]store.Adapter{"austiecodes/xxxx": &registryFakeAdapter{}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	_, err = registry.LS(context.Background(), store.LSRequest{Path: "/unknown"})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("LS(/unknown) error = %v, want ErrNotFound", err)
	}

	_, err = registry.Cat(context.Background(), store.CatRequest{Path: "/repos/austiecodes/missing.md"})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Cat(/repos/austiecodes/missing.md) error = %v, want ErrNotFound", err)
	}
}

func TestRegistryRejectsWritesToVirtualDirectories(t *testing.T) {
	registry, err := store.NewRegistry(map[string]store.Adapter{"austiecodes/xxxx": &registryFakeAdapter{}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	_, err = registry.Put(context.Background(), store.PutRequest{Path: "/repos", Content: "x"})
	if !errors.Is(err, store.ErrReadOnlyMount) {
		t.Fatalf("Put(/repos) error = %v, want ErrReadOnlyMount", err)
	}

	_, err = registry.Delete(context.Background(), store.DeleteRequest{Path: "/repos/austiecodes/xxxx"})
	if !errors.Is(err, store.ErrReadOnlyMount) {
		t.Fatalf("Delete(repo root) error = %v, want ErrReadOnlyMount", err)
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
		"rolio":             &registryFakeAdapter{},
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
	if sources[1].Ref != "repo://rolio" || sources[1].Kind != store.SourceKindRepo || sources[1].Name != "rolio" {
		t.Fatalf("source[1] = %+v, want rolio repo", sources[1])
	}
}

func TestNamespaceRegistryMountSourcesListsReposAndDocs(t *testing.T) {
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{
			"rolio":             &registryFakeAdapter{},
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
		{Ref: "repo://rolio", Kind: store.SourceKindRepo, Name: "rolio", Description: "repository namespace"},
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
		map[string]store.Adapter{"rolio": repoAdapter},
		map[string]store.Adapter{"openai-go-sdk": docsAdapter},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}

	gotRepo, err := registry.AdapterForSource(context.Background(), store.SourceRef{Kind: store.SourceKindRepo, Name: "rolio"})
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
		map[string]store.Adapter{"rolio": &registryFakeAdapter{}},
		map[string]store.Adapter{"": &registryFakeAdapter{}},
	)
	if err == nil {
		t.Fatal("NewNamespaceRegistry() error = nil, want empty docs namespace error")
	}

	_, err = store.NewNamespaceRegistry(
		map[string]store.Adapter{"rolio": &registryFakeAdapter{}},
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
		map[string]store.Adapter{"rolio": repoAdapter},
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

func assertNodePaths(t *testing.T, nodes []store.Node, want []string) {
	t.Helper()
	if len(nodes) != len(want) {
		t.Fatalf("node count = %d, want %d: %+v", len(nodes), len(want), nodes)
	}
	for i := range want {
		if nodes[i].Path != want[i] {
			t.Fatalf("node[%d].Path = %q, want %q; nodes = %+v", i, nodes[i].Path, want[i], nodes)
		}
	}
}

func assertAllDirs(t *testing.T, nodes []store.Node) {
	t.Helper()
	for _, node := range nodes {
		if node.Kind != "dir" {
			t.Fatalf("node = %+v, want dir", node)
		}
	}
}
