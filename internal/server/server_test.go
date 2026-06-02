package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/austiecodes/gxfs/internal/store"
)

type fakeAdapter struct {
	lsReq     store.LSRequest
	catReq    store.CatRequest
	grepReq   store.GrepRequest
	findReq   store.FindRequest
	treeReq   store.TreeRequest
	searchReq store.SearchRequest
	putReq    store.PutRequest
	usageReq  store.UsageEvent
	searchErr error
}

func (f *fakeAdapter) LS(_ context.Context, req store.LSRequest) (*store.LSResponse, error) {
	f.lsReq = req
	return &store.LSResponse{Nodes: []store.Node{{Path: "/docs", Name: "docs", Kind: "dir"}}}, nil
}

func (f *fakeAdapter) Tree(_ context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	f.treeReq = req
	return &store.TreeResponse{Text: "/\n"}, nil
}

func (f *fakeAdapter) Cat(_ context.Context, req store.CatRequest) (*store.CatResponse, error) {
	f.catReq = req
	content := "# Readme\n"
	return &store.CatResponse{Path: "/docs/readme.md", Content: content, Hash: store.HashContent(content)}, nil
}

func (f *fakeAdapter) Grep(_ context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	f.grepReq = req
	return &store.GrepResponse{Matches: []store.Match{{Path: "/go/store.go", Line: 12, Text: "type Adapter interface {"}}}, nil
}

func (f *fakeAdapter) Find(_ context.Context, req store.FindRequest) (*store.FindResponse, error) {
	f.findReq = req
	return &store.FindResponse{Nodes: []store.Node{{Path: "/go/store.go", Name: "store.go", Kind: "file"}}}, nil
}

func (f *fakeAdapter) Stat(context.Context, store.StatRequest) (*store.StatResponse, error) {
	return &store.StatResponse{Node: store.Node{Path: "/docs", Name: "docs", Kind: "dir"}}, nil
}

func (f *fakeAdapter) Put(_ context.Context, req store.PutRequest) (*store.PutResponse, error) {
	f.putReq = req
	return &store.PutResponse{Node: store.Node{Path: req.Path, Name: req.Path, Kind: "file"}}, nil
}

func (f *fakeAdapter) Delete(_ context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	return &store.DeleteResponse{}, nil
}

func (f *fakeAdapter) Edit(context.Context, store.EditRequest) (*store.EditResponse, error) {
	return nil, nil
}

func (f *fakeAdapter) BatchHashes(_ context.Context, _ store.HashRequest) (*store.HashResponse, error) {
	return &store.HashResponse{Hashes: []store.ContentHash{}}, nil
}

func (f *fakeAdapter) Search(_ context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	f.searchReq = req
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return &store.SearchResponse{
		Results: []store.SearchResult{
			{Path: "/docs/guide.md", Rank: 0.9, Snippet: "**test** result", Size: 100},
		},
		Total: 1,
	}, nil
}

func (f *fakeAdapter) Glob(_ context.Context, req store.GlobRequest) (*store.GlobResponse, error) {
	return &store.GlobResponse{Results: []store.GlobResult{}, Total: 0}, nil
}

func (f *fakeAdapter) Locate(_ context.Context, req store.LocateRequest) (*store.LocateResponse, error) {
	return &store.LocateResponse{Results: []store.LocateResult{}, Total: 0}, nil
}

func (f *fakeAdapter) RecordUsageEvent(_ context.Context, event store.UsageEvent) (*store.UsageEventResponse, error) {
	f.usageReq = event
	event.ID = "usage-1"
	event.CreatedAt = "2026-06-02T00:00:00Z"
	return &store.UsageEventResponse{Event: event}, nil
}

type fakeRegistryStore struct {
	mu            sync.Mutex
	repoSnapshots [][]store.RepoInfo
	registerErr   error
	listCalls     int
	registerCalls int
}

func (f *fakeRegistryStore) ListRepos(context.Context) ([]store.RepoInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	idx := f.listCalls
	f.listCalls++
	if len(f.repoSnapshots) == 0 {
		return []store.RepoInfo{}, nil
	}
	if idx >= len(f.repoSnapshots) {
		idx = len(f.repoSnapshots) - 1
	}
	repos := append([]store.RepoInfo(nil), f.repoSnapshots[idx]...)
	return repos, nil
}

func (f *fakeRegistryStore) RegisterRepo(_ context.Context, req store.RegisterRepoRequest) (*store.RegisterRepoResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.registerCalls++
	if f.registerErr != nil {
		return nil, f.registerErr
	}
	return &store.RegisterRepoResponse{Repo: store.RepoInfo{Name: req.Name, Writable: req.Writable}}, nil
}

func (f *fakeRegistryStore) setRepoSnapshots(snapshots [][]store.RepoInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repoSnapshots = snapshots
}

func (f *fakeRegistryStore) listCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls
}

func (f *fakeRegistryStore) registerCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registerCalls
}

func (f *fakeRegistryStore) ListDocNamespaces(context.Context) ([]store.DocNamespace, error) {
	return []store.DocNamespace{}, nil
}

func (f *fakeRegistryStore) ListDocsets(context.Context) ([]store.Docset, error) {
	return []store.Docset{}, nil
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition was not met within %s", timeout)
}

func TestHandlerRoutesLS(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/ls?repo=gxfs&path=/docs", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if adapter.lsReq.Repo != "gxfs" || adapter.lsReq.Path != "/docs" {
		t.Fatalf("ls req = %+v, want gxfs /docs", adapter.lsReq)
	}
	var resp store.LSResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Name != "docs" {
		t.Fatalf("resp = %+v, want docs node", resp)
	}
}

func TestHandlerRecordsUsageEvent(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodPost, "/v1/usage-events", strings.NewReader(`{
		"session_id":"session-1",
		"client_repo":"gxfs",
		"command":"search",
		"exit_code":0,
		"duration_ms":42,
		"payload":{"query":"auth"}
	}`))
	req.Header.Set("X-Gxfs-Log-Id", "log-1")
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if adapter.usageReq.LogID != "log-1" {
		t.Fatalf("usage log_id = %q, want log-1", adapter.usageReq.LogID)
	}
	if adapter.usageReq.SessionID != "session-1" || adapter.usageReq.ClientRepo != "gxfs" {
		t.Fatalf("usage req = %+v, want session/client repo", adapter.usageReq)
	}
	if adapter.usageReq.Command != "search" || adapter.usageReq.ExitCode != 0 || adapter.usageReq.DurationMs != 42 {
		t.Fatalf("usage req = %+v, want search success duration", adapter.usageReq)
	}
	var resp store.UsageEventResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Event.ID != "usage-1" {
		t.Fatalf("usage response ID = %q, want usage-1", resp.Event.ID)
	}
}

func TestHandlerRegisterRepoDuplicateMapsConflict(t *testing.T) {
	catalog := &fakeRegistryStore{registerErr: store.ErrRepoExists}
	registry, err := NewDynamicRegistry(context.Background(), catalog, catalog, func(context.Context, DynamicSource) (store.Adapter, error) {
		return &fakeAdapter{}, nil
	})
	if err != nil {
		t.Fatalf("NewDynamicRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/repos", strings.NewReader(`{"name":"austiecodes/xxxx"}`))
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := catalog.registerCallCount(); got != 1 {
		t.Fatalf("RegisterRepo calls = %d, want 1", got)
	}
	if !strings.Contains(rec.Body.String(), `"code":"REPO_EXISTS"`) {
		t.Fatalf("body = %q, want REPO_EXISTS code", rec.Body.String())
	}
}

func TestHandlerRegisterRepoCreatedRefreshesRegistry(t *testing.T) {
	catalog := &fakeRegistryStore{
		repoSnapshots: [][]store.RepoInfo{
			{},
			{{Name: "austiecodes/xxxx", Writable: true}},
		},
	}
	registry, err := NewDynamicRegistry(context.Background(), catalog, catalog, func(context.Context, DynamicSource) (store.Adapter, error) {
		return &fakeAdapter{}, nil
	})
	if err != nil {
		t.Fatalf("NewDynamicRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/repos", strings.NewReader(`{"name":"austiecodes/xxxx","writable":true}`))
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := catalog.listCallCount(); got != 2 {
		t.Fatalf("ListRepos calls = %d, want initial load plus post-register refresh", got)
	}
	if !registry.RepoWritable("austiecodes/xxxx") {
		t.Fatal("registered repo was not refreshed into writable registry cache")
	}
}

func TestDynamicRegistryRefreshesOnceForUnknownRepo(t *testing.T) {
	const repoName = "github.com/austiecodes/xxxx"
	catalog := &fakeRegistryStore{
		repoSnapshots: [][]store.RepoInfo{
			{},
			{{Name: repoName}},
		},
	}
	adapters := map[string]*fakeAdapter{}
	registry, err := NewDynamicRegistry(context.Background(), catalog, catalog, func(_ context.Context, source DynamicSource) (store.Adapter, error) {
		adapter := &fakeAdapter{}
		adapters[source.Name] = adapter
		return adapter, nil
	})
	if err != nil {
		t.Fatalf("NewDynamicRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/ls?repo=github.com%2Faustiecodes%2Fxxxx&path=/docs", nil)
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := catalog.listCallCount(); got != 2 {
		t.Fatalf("ListRepos calls = %d, want initial load plus one unknown refresh", got)
	}
	adapter := adapters[repoName]
	if adapter == nil {
		t.Fatalf("adapter for %s was not created", repoName)
	}
	if adapter.lsReq.Repo != repoName || adapter.lsReq.Path != "/docs" {
		t.Fatalf("ls req = %+v, want %s /docs", adapter.lsReq, repoName)
	}
}

func TestDynamicRegistryRefreshesBeforeReturningUnknownRepo(t *testing.T) {
	catalog := &fakeRegistryStore{
		repoSnapshots: [][]store.RepoInfo{{}},
	}
	registry, err := NewDynamicRegistry(context.Background(), catalog, catalog, func(context.Context, DynamicSource) (store.Adapter, error) {
		return &fakeAdapter{}, nil
	})
	if err != nil {
		t.Fatalf("NewDynamicRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/ls?repo=missing&path=/docs", nil)
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := catalog.listCallCount(); got != 2 {
		t.Fatalf("ListRepos calls = %d, want initial load plus one unknown refresh", got)
	}
	if !strings.Contains(rec.Body.String(), `"code":"UNKNOWN_REPO"`) {
		t.Fatalf("body = %q, want UNKNOWN_REPO code", rec.Body.String())
	}
}

func TestDynamicRegistryRoutesUnifiedRoot(t *testing.T) {
	catalog := &fakeRegistryStore{
		repoSnapshots: [][]store.RepoInfo{{{Name: "austiecodes/xxxx"}}},
	}
	adapters := map[string]*fakeAdapter{}
	registry, err := NewDynamicRegistry(context.Background(), catalog, catalog, func(_ context.Context, source DynamicSource) (store.Adapter, error) {
		adapter := &fakeAdapter{}
		adapters[source.Name] = adapter
		return adapter, nil
	})
	if err != nil {
		t.Fatalf("NewDynamicRegistry() error = %v", err)
	}

	resp, err := registry.LS(context.Background(), store.LSRequest{Path: "/repos/austiecodes/xxxx/docs"})
	if err != nil {
		t.Fatalf("LS(unified root) error = %v", err)
	}
	adapter := adapters["austiecodes/xxxx"]
	if adapter == nil {
		t.Fatal("adapter for austiecodes/xxxx was not created")
	}
	if adapter.lsReq.Repo != "austiecodes/xxxx" || adapter.lsReq.Path != "/docs" {
		t.Fatalf("ls req = %+v, want austiecodes/xxxx /docs", adapter.lsReq)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Path != "/repos/austiecodes/xxxx/docs" {
		t.Fatalf("LS response = %+v, want localized unified path", resp)
	}
}

func TestCrossRepoWritableGateRefreshesDynamicRegistryMiss(t *testing.T) {
	catalog := &fakeRegistryStore{
		repoSnapshots: [][]store.RepoInfo{
			{},
			{{Name: "target", Writable: true}},
		},
	}
	adapters := map[string]*fakeAdapter{}
	registry, err := NewDynamicRegistry(context.Background(), catalog, catalog, func(_ context.Context, source DynamicSource) (store.Adapter, error) {
		adapter := &fakeAdapter{}
		adapters[source.Name] = adapter
		return adapter, nil
	})
	if err != nil {
		t.Fatalf("NewDynamicRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/repos/write?repo=target&path=/docs/new.md", strings.NewReader("hello"))
	req.Header.Set("X-Client-Repo", "client")
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := catalog.listCallCount(); got != 2 {
		t.Fatalf("ListRepos calls = %d, want initial load plus writable-gate refresh", got)
	}
	adapter := adapters["target"]
	if adapter == nil {
		t.Fatal("adapter for target was not created")
	}
	if adapter.putReq.Repo != "target" || adapter.putReq.Path != "/docs/new.md" || adapter.putReq.Content != "hello" {
		t.Fatalf("put req = %+v, want refreshed writable target write", adapter.putReq)
	}
}

func TestDynamicRegistryStartRefreshLoopUpdatesCache(t *testing.T) {
	catalog := &fakeRegistryStore{
		repoSnapshots: [][]store.RepoInfo{{}},
	}
	registry, err := NewDynamicRegistry(context.Background(), catalog, catalog, func(context.Context, DynamicSource) (store.Adapter, error) {
		return &fakeAdapter{}, nil
	})
	if err != nil {
		t.Fatalf("NewDynamicRegistry() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := registry.StartRefreshLoop(ctx, time.Millisecond)
	catalog.setRepoSnapshots([][]store.RepoInfo{{{Name: "periodic", Writable: true}}})

	waitUntil(t, 250*time.Millisecond, func() bool {
		return registry.RepoWritable("periodic")
	})
	cancel()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("refresh loop did not stop after context cancellation")
	}
}

func TestHandlerRoutesDocsNamespaceToDocsAdapter(t *testing.T) {
	repoAdapter := &fakeAdapter{}
	docsAdapter := &fakeAdapter{}
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": repoAdapter},
		map[string]store.Adapter{"openai-go-sdk": docsAdapter},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/docs/cat?name=openai-go-sdk&path=/usage.md", nil)
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if docsAdapter.catReq.Repo != "openai-go-sdk" || docsAdapter.catReq.Path != "/usage.md" {
		t.Fatalf("docs cat req = %+v, want openai-go-sdk /usage.md", docsAdapter.catReq)
	}
	if repoAdapter.catReq != (store.CatRequest{}) {
		t.Fatalf("repo adapter cat req = %+v, want untouched", repoAdapter.catReq)
	}
}

func TestHandlerRoutesRepoNamespaceToRepoAdapter(t *testing.T) {
	repoAdapter := &fakeAdapter{}
	docsAdapter := &fakeAdapter{}
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": repoAdapter},
		map[string]store.Adapter{"openai-go-sdk": docsAdapter},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/cat?repo=gxfs&path=/readme.md", nil)
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if repoAdapter.catReq.Repo != "gxfs" || repoAdapter.catReq.Path != "/readme.md" {
		t.Fatalf("repo cat req = %+v, want gxfs /readme.md", repoAdapter.catReq)
	}
	if docsAdapter.catReq != (store.CatRequest{}) {
		t.Fatalf("docs adapter cat req = %+v, want untouched", docsAdapter.catReq)
	}
}

func TestHandlerDocsNamespaceRequiresSourceRouter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/docs/cat?name=openai-go-sdk&path=/usage.md", nil)
	rec := httptest.NewRecorder()

	NewHandler(&fakeAdapter{}, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"NOT_SUPPORTED"`) {
		t.Fatalf("body = %q, want NOT_SUPPORTED code", rec.Body.String())
	}
}

func TestHandlerUnknownDocsNamespaceMapsUnknownSource(t *testing.T) {
	registry, err := store.NewNamespaceRegistry(
		map[string]store.Adapter{"gxfs": &fakeAdapter{}},
		map[string]store.Adapter{"openai-go-sdk": &fakeAdapter{}},
	)
	if err != nil {
		t.Fatalf("NewNamespaceRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/docs/cat?name=missing&path=/usage.md", nil)
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"UNKNOWN_SOURCE"`) {
		t.Fatalf("body = %q, want UNKNOWN_SOURCE code", rec.Body.String())
	}
}

func TestHandlerMountSourcesListsRepoSources(t *testing.T) {
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"gxfs":             &fakeAdapter{},
		"github/openai-go": &fakeAdapter{},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/mount-sources", nil)
	rec := httptest.NewRecorder()

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Sources []store.MountSource `json:"sources"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Sources) != 2 {
		t.Fatalf("sources len = %d, want 2: %+v", len(resp.Sources), resp.Sources)
	}
	if resp.Sources[0].Ref != "repo://github%2Fopenai-go" || resp.Sources[0].Kind != store.SourceKindRepo || resp.Sources[0].Name != "github/openai-go" {
		t.Fatalf("source[0] = %+v, want escaped repo source", resp.Sources[0])
	}
	if resp.Sources[1].Ref != "repo://gxfs" || resp.Sources[1].Kind != store.SourceKindRepo || resp.Sources[1].Name != "gxfs" {
		t.Fatalf("source[1] = %+v, want gxfs repo source", resp.Sources[1])
	}
}

func TestHandlerRoutesLSParams(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want store.LSRequest
	}{
		{
			name: "defaults",
			url:  "/v1/repos/ls?repo=test&path=/docs",
			want: store.LSRequest{Repo: "test", Path: "/docs"},
		},
		{
			name: "sort",
			url:  "/v1/repos/ls?repo=test&path=/docs&sort=size",
			want: store.LSRequest{Repo: "test", Path: "/docs", Sort: "size"},
		},
		{
			name: "reverse",
			url:  "/v1/repos/ls?repo=test&path=/docs&reverse=true",
			want: store.LSRequest{Repo: "test", Path: "/docs", Reverse: true},
		},
		{
			name: "recursive",
			url:  "/v1/repos/ls?repo=test&path=/docs&recursive=true",
			want: store.LSRequest{Repo: "test", Path: "/docs", Recursive: true},
		},
		{
			name: "all",
			url:  "/v1/repos/ls?repo=test&path=/docs&all=true",
			want: store.LSRequest{Repo: "test", Path: "/docs", All: true},
		},
		{
			name: "invalid reverse treated as false",
			url:  "/v1/repos/ls?repo=test&path=/docs&reverse=garbage",
			want: store.LSRequest{Repo: "test", Path: "/docs"},
		},
		{
			name: "invalid recursive treated as false",
			url:  "/v1/repos/ls?repo=test&path=/docs&recursive=nope",
			want: store.LSRequest{Repo: "test", Path: "/docs"},
		},
		{
			name: "invalid all treated as false",
			url:  "/v1/repos/ls?repo=test&path=/docs&all=bogus",
			want: store.LSRequest{Repo: "test", Path: "/docs"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &fakeAdapter{}
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			NewHandler(adapter, nil).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			got := adapter.lsReq
			if got.Repo != tt.want.Repo || got.Path != tt.want.Path ||
				got.Sort != tt.want.Sort || got.Reverse != tt.want.Reverse ||
				got.Recursive != tt.want.Recursive || got.All != tt.want.All {
				t.Fatalf("ls req = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHandlerRoutesGrepRegex(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/grep?repo=gxfs&path=/go&pattern=Adapter&regex=true", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if adapter.grepReq.Repo != "gxfs" || adapter.grepReq.Path != "/go" ||
		adapter.grepReq.Pattern != "Adapter" || !adapter.grepReq.Regex {
		t.Fatalf("grep req = %+v, want regex grep", adapter.grepReq)
	}
}

func TestHandlerRoutesGrepParams(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want store.GrepRequest
	}{
		{
			name: "defaults",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello"},
		},
		{
			name: "case_insensitive",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&case_insensitive=true",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", CaseInsensitive: true},
		},
		{
			name: "invert",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&invert=true",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", Invert: true},
		},
		{
			name: "whole_word",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&whole_word=true",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", WholeWord: true},
		},
		{
			name: "whole_line",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&whole_line=true",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", WholeLine: true},
		},
		{
			name: "context_before",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&context_before=3",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", ContextBefore: 3},
		},
		{
			name: "context_after",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&context_after=5",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", ContextAfter: 5},
		},
		{
			name: "all",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&all=true",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", All: true},
		},
		{
			name: "include",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&include=*.go",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", Include: "*.go"},
		},
		{
			name: "exclude",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&exclude=*.md",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello", Exclude: "*.md"},
		},
		{
			name: "invalid bool treated as false",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&case_insensitive=garbage&invert=nope",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello"},
		},
		{
			name: "invalid int treated as zero",
			url:  "/v1/repos/grep?repo=test&path=/src&pattern=hello&context_before=abc&context_after=xyz",
			want: store.GrepRequest{Repo: "test", Path: "/src", Pattern: "hello"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &fakeAdapter{}
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			NewHandler(adapter, nil).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			got := adapter.grepReq
			if got != tt.want {
				t.Fatalf("grep req = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHandlerHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeAdapter{}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Fatalf("healthz = %d %q, want ok", rec.Code, rec.Body.String())
	}
}

func TestHandlerRoutesFindParams(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want store.FindRequest
	}{
		{
			name: "defaults",
			url:  "/v1/repos/find?repo=test&path=/src",
			want: store.FindRequest{Repo: "test", Path: "/src"},
		},
		{
			name: "name",
			url:  "/v1/repos/find?repo=test&path=/src&name=*.go",
			want: store.FindRequest{Repo: "test", Path: "/src", Name: "*.go"},
		},
		{
			name: "type",
			url:  "/v1/repos/find?repo=test&path=/src&type=dir",
			want: store.FindRequest{Repo: "test", Path: "/src", Type: "dir"},
		},
		{
			name: "maxdepth",
			url:  "/v1/repos/find?repo=test&path=/src&maxdepth=3",
			want: store.FindRequest{Repo: "test", Path: "/src", MaxDepth: 3},
		},
		{
			name: "mindepth",
			url:  "/v1/repos/find?repo=test&path=/src&mindepth=2",
			want: store.FindRequest{Repo: "test", Path: "/src", MinDepth: 2},
		},
		{
			name: "all",
			url:  "/v1/repos/find?repo=test&path=/src&all=true",
			want: store.FindRequest{Repo: "test", Path: "/src", All: true},
		},
		{
			name: "iname",
			url:  "/v1/repos/find?repo=test&path=/src&iname=README",
			want: store.FindRequest{Repo: "test", Path: "/src", IName: "README"},
		},
		{
			name: "all params combined",
			url:  "/v1/repos/find?repo=test&path=/src&name=*.go&type=file&maxdepth=5&mindepth=1&all=true&iname=readme",
			want: store.FindRequest{Repo: "test", Path: "/src", Name: "*.go", Type: "file", MaxDepth: 5, MinDepth: 1, All: true, IName: "readme"},
		},
		{
			name: "invalid bool treated as false",
			url:  "/v1/repos/find?repo=test&path=/src&all=garbage",
			want: store.FindRequest{Repo: "test", Path: "/src"},
		},
		{
			name: "invalid int treated as zero",
			url:  "/v1/repos/find?repo=test&path=/src&maxdepth=abc&mindepth=xyz",
			want: store.FindRequest{Repo: "test", Path: "/src"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &fakeAdapter{}
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			NewHandler(adapter, nil).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			got := adapter.findReq
			if got != tt.want {
				t.Fatalf("find req = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHandlerRoutesTreeParams(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		want        store.TreeRequest
		expectError bool
	}{
		{
			name: "defaults",
			url:  "/v1/repos/tree?repo=test&path=/src",
			want: store.TreeRequest{Repo: "test", Path: "/src"},
		},
		{
			name: "depth",
			url:  "/v1/repos/tree?repo=test&path=/src&depth=2",
			want: store.TreeRequest{Repo: "test", Path: "/src", Depth: 2},
		},
		{
			name: "all",
			url:  "/v1/repos/tree?repo=test&path=/src&all=true",
			want: store.TreeRequest{Repo: "test", Path: "/src", All: true},
		},
		{
			name: "dirs_only",
			url:  "/v1/repos/tree?repo=test&path=/src&dirs_only=true",
			want: store.TreeRequest{Repo: "test", Path: "/src", DirsOnly: true},
		},
		{
			name: "full_path",
			url:  "/v1/repos/tree?repo=test&path=/src&full_path=true",
			want: store.TreeRequest{Repo: "test", Path: "/src", FullPath: true},
		},
		{
			name: "show_size",
			url:  "/v1/repos/tree?repo=test&path=/src&show_size=true",
			want: store.TreeRequest{Repo: "test", Path: "/src", ShowSize: true},
		},
		{
			name: "sort",
			url:  "/v1/repos/tree?repo=test&path=/src&sort=size",
			want: store.TreeRequest{Repo: "test", Path: "/src", Sort: "size"},
		},
		{
			name: "dirs_first",
			url:  "/v1/repos/tree?repo=test&path=/src&dirs_first=true",
			want: store.TreeRequest{Repo: "test", Path: "/src", DirsFirst: true},
		},
		{
			name: "all params combined",
			url:  "/v1/repos/tree?repo=test&path=/src&depth=3&all=true&dirs_only=true&full_path=true&show_size=true&sort=mtime&dirs_first=true",
			want: store.TreeRequest{Repo: "test", Path: "/src", Depth: 3, All: true, DirsOnly: true, FullPath: true, ShowSize: true, Sort: "mtime", DirsFirst: true},
		},
		{
			name: "invalid bool treated as false",
			url:  "/v1/repos/tree?repo=test&path=/src&all=garbage&dirs_only=nope",
			want: store.TreeRequest{Repo: "test", Path: "/src"},
		},
		{
			name:        "invalid depth returns error",
			url:         "/v1/repos/tree?repo=test&path=/src&depth=abc",
			want:        store.TreeRequest{}, // won't match; we check status != 200
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &fakeAdapter{}
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			NewHandler(adapter, nil).ServeHTTP(rec, req)

			if tt.expectError {
				if rec.Code == http.StatusOK {
					t.Fatal("expected error status, got 200")
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			got := adapter.treeReq
			if got != tt.want {
				t.Fatalf("tree req = %+v, want %+v", got, tt.want)
			}
		})
	}
}

type readOnlyAdapter struct {
	fakeAdapter
}

func (a *readOnlyAdapter) Put(context.Context, store.PutRequest) (*store.PutResponse, error) {
	return nil, store.ErrReadOnlyMount
}

func (a *readOnlyAdapter) Search(_ context.Context, _ store.SearchRequest) (*store.SearchResponse, error) {
	return &store.SearchResponse{Results: []store.SearchResult{}}, nil
}

func (a *readOnlyAdapter) Glob(_ context.Context, _ store.GlobRequest) (*store.GlobResponse, error) {
	return &store.GlobResponse{Results: []store.GlobResult{}}, nil
}

func (a *readOnlyAdapter) Locate(_ context.Context, _ store.LocateRequest) (*store.LocateResponse, error) {
	return &store.LocateResponse{Results: []store.LocateResult{}}, nil
}

func TestHandlerMapsReadOnlyMountErrorToForbidden(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/v1/repos/write?repo=gxfs&path=/docs/readme.md", strings.NewReader("hello"))
	rec := httptest.NewRecorder()

	NewHandler(&readOnlyAdapter{}, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"FORBIDDEN"`) {
		t.Fatalf("body = %q, want FORBIDDEN code", rec.Body.String())
	}
}

func TestHandlerMapsUnknownRepoToNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/ls?repo=missing&path=/docs", nil)
	rec := httptest.NewRecorder()
	registry, err := store.NewRegistry(map[string]store.Adapter{"known": &fakeAdapter{}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	NewHandler(registry, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"UNKNOWN_REPO"`) {
		t.Fatalf("body = %q, want UNKNOWN_REPO code", rec.Body.String())
	}
}

func TestHandlerRoutesSearch(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/search?repo=gxfs&q=test&path=/docs&limit=5", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if adapter.searchReq.Query != "test" {
		t.Fatalf("search query = %q, want %q", adapter.searchReq.Query, "test")
	}
	if adapter.searchReq.Path != "/docs" {
		t.Fatalf("search path = %q, want %q", adapter.searchReq.Path, "/docs")
	}
	if adapter.searchReq.Limit != 5 {
		t.Fatalf("search limit = %d, want 5", adapter.searchReq.Limit)
	}
	if adapter.searchReq.Repo != "gxfs" {
		t.Fatalf("search repo = %q, want %q", adapter.searchReq.Repo, "gxfs")
	}

	var resp store.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 1 || len(resp.Results) != 1 {
		t.Fatalf("search response = %+v, want 1 result", resp)
	}
	if resp.Results[0].Path != "/docs/guide.md" {
		t.Fatalf("result path = %q, want /docs/guide.md", resp.Results[0].Path)
	}
}

func TestHandlerSearchEmptyQuery(t *testing.T) {
	adapter := &fakeAdapter{searchErr: store.ErrEmptyQuery}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/search?repo=gxfs&q=", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "BAD_REQUEST") {
		t.Fatalf("body = %q, want BAD_REQUEST", rec.Body.String())
	}
}

func TestHandlerSearchInvalidLimit(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/search?repo=gxfs&q=test&limit=abc", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid limit", rec.Code)
	}
}

// TestHandlerCatContentNotFound verifies that Cat returns 404 when the
// underlying adapter returns ErrNotFound (e.g. after delete).
func TestHandlerCatContentNotFound(t *testing.T) {
	adapter := &notFoundCatAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/cat?repo=gxfs&path=/docs/deleted.md", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("body = %q, want NOT_FOUND code", rec.Body.String())
	}
}

type notFoundCatAdapter struct {
	fakeAdapter
}

func (n *notFoundCatAdapter) Cat(context.Context, store.CatRequest) (*store.CatResponse, error) {
	return nil, store.ErrNotFound
}

func TestHandlerLSLimitOffset(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantLimit   int
		wantOffset  int
		expectError bool
	}{
		{
			name:       "defaults",
			url:        "/v1/repos/ls?repo=test&path=/docs",
			wantLimit:  0,
			wantOffset: 0,
		},
		{
			name:       "limit",
			url:        "/v1/repos/ls?repo=test&path=/docs&limit=10",
			wantLimit:  10,
			wantOffset: 0,
		},
		{
			name:       "offset",
			url:        "/v1/repos/ls?repo=test&path=/docs&offset=20",
			wantLimit:  0,
			wantOffset: 20,
		},
		{
			name:       "both",
			url:        "/v1/repos/ls?repo=test&path=/docs&limit=5&offset=10",
			wantLimit:  5,
			wantOffset: 10,
		},
		{
			name:        "negative limit rejected",
			url:         "/v1/repos/ls?repo=test&path=/docs&limit=-1",
			expectError: true,
		},
		{
			name:        "negative offset rejected",
			url:         "/v1/repos/ls?repo=test&path=/docs&offset=-5",
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &fakeAdapter{}
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			NewHandler(adapter, nil).ServeHTTP(rec, req)

			if tt.expectError {
				if rec.Code == http.StatusOK {
					t.Fatal("expected error status, got 200")
				}
				if !strings.Contains(rec.Body.String(), "BAD_REQUEST") {
					t.Fatalf("body = %q, want BAD_REQUEST", rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if adapter.lsReq.Limit != tt.wantLimit {
				t.Fatalf("limit = %d, want %d", adapter.lsReq.Limit, tt.wantLimit)
			}
			if adapter.lsReq.Offset != tt.wantOffset {
				t.Fatalf("offset = %d, want %d", adapter.lsReq.Offset, tt.wantOffset)
			}
		})
	}
}

func TestHandlerFindLimitOffset(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantLimit   int
		wantOffset  int
		expectError bool
	}{
		{
			name:       "defaults",
			url:        "/v1/repos/find?repo=test&path=/src&name=*.go",
			wantLimit:  0,
			wantOffset: 0,
		},
		{
			name:       "limit",
			url:        "/v1/repos/find?repo=test&path=/src&name=*.go&limit=10",
			wantLimit:  10,
			wantOffset: 0,
		},
		{
			name:       "offset",
			url:        "/v1/repos/find?repo=test&path=/src&name=*.go&offset=5",
			wantLimit:  0,
			wantOffset: 5,
		},
		{
			name:        "negative limit rejected",
			url:         "/v1/repos/find?repo=test&path=/src&name=*.go&limit=-1",
			expectError: true,
		},
		{
			name:        "negative offset rejected",
			url:         "/v1/repos/find?repo=test&path=/src&name=*.go&offset=-3",
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &fakeAdapter{}
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			NewHandler(adapter, nil).ServeHTTP(rec, req)

			if tt.expectError {
				if rec.Code == http.StatusOK {
					t.Fatal("expected error status, got 200")
				}
				if !strings.Contains(rec.Body.String(), "BAD_REQUEST") {
					t.Fatalf("body = %q, want BAD_REQUEST", rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if adapter.findReq.Limit != tt.wantLimit {
				t.Fatalf("limit = %d, want %d", adapter.findReq.Limit, tt.wantLimit)
			}
			if adapter.findReq.Offset != tt.wantOffset {
				t.Fatalf("offset = %d, want %d", adapter.findReq.Offset, tt.wantOffset)
			}
		})
	}
}

func TestHandlerSearchLimitOffset(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantLimit   int
		wantOffset  int
		expectError bool
	}{
		{
			name:       "defaults",
			url:        "/v1/repos/search?repo=test&q=hello",
			wantLimit:  0,
			wantOffset: 0,
		},
		{
			name:       "limit",
			url:        "/v1/repos/search?repo=test&q=hello&limit=10",
			wantLimit:  10,
			wantOffset: 0,
		},
		{
			name:       "offset",
			url:        "/v1/repos/search?repo=test&q=hello&offset=20",
			wantLimit:  0,
			wantOffset: 20,
		},
		{
			name:       "both",
			url:        "/v1/repos/search?repo=test&q=hello&limit=5&offset=10",
			wantLimit:  5,
			wantOffset: 10,
		},
		{
			name:        "negative limit rejected",
			url:         "/v1/repos/search?repo=test&q=hello&limit=-1",
			expectError: true,
		},
		{
			name:        "negative offset rejected",
			url:         "/v1/repos/search?repo=test&q=hello&offset=-5",
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &fakeAdapter{}
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()

			NewHandler(adapter, nil).ServeHTTP(rec, req)

			if tt.expectError {
				if rec.Code == http.StatusOK {
					t.Fatal("expected error status, got 200")
				}
				if !strings.Contains(rec.Body.String(), "BAD_REQUEST") {
					t.Fatalf("body = %q, want BAD_REQUEST", rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if adapter.searchReq.Limit != tt.wantLimit {
				t.Fatalf("limit = %d, want %d", adapter.searchReq.Limit, tt.wantLimit)
			}
			if adapter.searchReq.Offset != tt.wantOffset {
				t.Fatalf("offset = %d, want %d", adapter.searchReq.Offset, tt.wantOffset)
			}
		})
	}
}

// --- ETag / If-None-Match tests ---

func TestHandlerCatReturnsETag(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/cat?repo=gxfs&path=/docs/readme.md", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag header missing")
	}
	wantHash := store.HashContent("# Readme\n")
	wantETag := `"` + wantHash + `"`
	if etag != wantETag {
		t.Fatalf("ETag = %q, want %q", etag, wantETag)
	}
	var resp store.CatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Content != "# Readme\n" {
		t.Fatalf("content = %q, want readme content", resp.Content)
	}
}

func TestHandlerCatIfNoneMatchReturns304(t *testing.T) {
	adapter := &fakeAdapter{}
	hash := store.HashContent("# Readme\n")
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/cat?repo=gxfs&path=/docs/readme.md", nil)
	req.Header.Set("If-None-Match", `"`+hash+`"`)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusNotModified, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("304 body should be empty, got %q", rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	if etag != `"`+hash+`"` {
		t.Fatalf("304 ETag = %q, want quoted hash", etag)
	}
}

func TestHandlerCatIfNoneMatchMismatchReturns200(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/cat?repo=gxfs&path=/docs/readme.md", nil)
	req.Header.Set("If-None-Match", `"sha256:0000000000"`)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp store.CatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Content != "# Readme\n" {
		t.Fatalf("content = %q, want readme content", resp.Content)
	}
}

func TestHandlerCatIfNoneMatchUnquoted(t *testing.T) {
	adapter := &fakeAdapter{}
	hash := store.HashContent("# Readme\n")
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/cat?repo=gxfs&path=/docs/readme.md", nil)
	req.Header.Set("If-None-Match", hash)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d (unquoted ETag should match)", rec.Code, http.StatusNotModified)
	}
}

func TestHandlerCatIfNoneMatchMultipleETags(t *testing.T) {
	adapter := &fakeAdapter{}
	hash := store.HashContent("# Readme\n")
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/cat?repo=gxfs&path=/docs/readme.md", nil)
	req.Header.Set("If-None-Match", `"sha256:other", "`+hash+`"`)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d (comma-separated ETags should match)", rec.Code, http.StatusNotModified)
	}
}

func TestHandlerCatNoHashNoETag(t *testing.T) {
	adapter := &noHashCatAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/cat?repo=gxfs&path=/docs/readme.md", nil)
	req.Header.Set("If-None-Match", `"sha256:whatever"`)
	rec := httptest.NewRecorder()

	NewHandler(adapter, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (no hash -> normal response)", rec.Code, http.StatusOK)
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Fatalf("ETag should be empty when adapter returns no hash, got %q", etag)
	}
}

type noHashCatAdapter struct {
	fakeAdapter
}

func (n *noHashCatAdapter) Cat(context.Context, store.CatRequest) (*store.CatResponse, error) {
	return &store.CatResponse{Path: "/docs/readme.md", Content: "no hash"}, nil
}

// --- Docset route tests ---

type fakeDocsetManager struct {
	docsets          map[string]store.Docset
	members          map[string][]store.DocsetMember // key: docset name
	createErr        error
	getErr           error
	listErr          error
	deleteErr        error
	addMemberErr     error
	removeMemberErr  error
	getContentResp   *store.GetDocsetMemberContentResponse
	getContentErr    error
	lastAddMemberReq store.AddDocsetMemberRequest
}

func newFakeDocsetManager() *fakeDocsetManager {
	return &fakeDocsetManager{
		docsets: make(map[string]store.Docset),
		members: make(map[string][]store.DocsetMember),
	}
}

func (f *fakeDocsetManager) CreateDocset(_ context.Context, req store.CreateDocsetRequest) (*store.CreateDocsetResponse, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	col := store.Docset{
		ID:          "test-id-" + req.Name,
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   "2024-01-01T00:00:00Z",
		UpdatedAt:   "2024-01-01T00:00:00Z",
	}
	f.docsets[req.Name] = col
	return &store.CreateDocsetResponse{Docset: col}, nil
}

func (f *fakeDocsetManager) ListDocsets(context.Context) (*store.ListDocsetsResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var cols []store.Docset
	for _, col := range f.docsets {
		cols = append(cols, col)
	}
	if cols == nil {
		cols = []store.Docset{}
	}
	return &store.ListDocsetsResponse{Docsets: cols}, nil
}

func (f *fakeDocsetManager) GetDocset(_ context.Context, name string) (*store.GetDocsetResponse, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	col, ok := f.docsets[name]
	if !ok {
		return nil, store.ErrDocsetNotFound
	}
	members := f.members[name]
	if members == nil {
		members = []store.DocsetMember{}
	}
	return &store.GetDocsetResponse{Docset: col, Members: members}, nil
}

func (f *fakeDocsetManager) DeleteDocset(_ context.Context, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.docsets[name]; !ok {
		return store.ErrDocsetNotFound
	}
	delete(f.docsets, name)
	delete(f.members, name)
	return nil
}

func (f *fakeDocsetManager) AddDocsetMember(_ context.Context, req store.AddDocsetMemberRequest) (*store.AddDocsetMemberResponse, error) {
	f.lastAddMemberReq = req
	if f.addMemberErr != nil {
		return nil, f.addMemberErr
	}
	if _, ok := f.docsets[req.Name]; !ok {
		return nil, store.ErrDocsetNotFound
	}
	member := store.DocsetMember{Path: req.Path, DocID: "doc-123"}
	f.members[req.Name] = append(f.members[req.Name], member)
	return &store.AddDocsetMemberResponse{Member: member}, nil
}

func (f *fakeDocsetManager) RemoveDocsetMember(_ context.Context, req store.RemoveDocsetMemberRequest) error {
	if f.removeMemberErr != nil {
		return f.removeMemberErr
	}
	members := f.members[req.Name]
	for i, m := range members {
		if m.Path == req.Path {
			f.members[req.Name] = append(members[:i], members[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeDocsetManager) GetDocsetMemberContent(_ context.Context, req store.GetDocsetMemberContentRequest) (*store.GetDocsetMemberContentResponse, error) {
	if f.getContentErr != nil {
		return nil, f.getContentErr
	}
	if f.getContentResp != nil {
		return f.getContentResp, nil
	}
	return &store.GetDocsetMemberContentResponse{Path: req.Path, Content: "test content", Hash: "hash123"}, nil
}

func TestHandlerDocsetRoutes(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		url        string
		body       string
		wantStatus int
		checkBody  func(t *testing.T, body string)
	}{
		{
			name:       "create docset",
			method:     http.MethodPost,
			url:        "/v1/docsets",
			body:       `{"name":"test-col","description":"test description"}`,
			wantStatus: http.StatusOK, // server returns 200, not 201
		},
		{
			name:       "list docsets",
			method:     http.MethodGet,
			url:        "/v1/docsets",
			wantStatus: http.StatusOK,
		},
		{
			name:       "get docset",
			method:     http.MethodGet,
			url:        "/v1/docsets/test-col",
			wantStatus: http.StatusOK,
		},
		{
			name:       "delete docset",
			method:     http.MethodDelete,
			url:        "/v1/docsets/test-col",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "add member",
			method:     http.MethodPut,
			url:        "/v1/docsets/test-col/members",
			body:       `{"source_ref":"repo://test-repo/docs/readme.md","path":"/readme.md"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "get member content",
			method:     http.MethodGet,
			url:        "/v1/docsets/test-col/docs?path=/readme.md",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			colMgr := newFakeDocsetManager()
			// Pre-create test-col for get/delete/member tests
			colMgr.docsets["test-col"] = store.Docset{
				ID:          "test-id",
				Name:        "test-col",
				Description: "test",
				CreatedAt:   "2024-01-01T00:00:00Z",
				UpdatedAt:   "2024-01-01T00:00:00Z",
			}
			// Pre-add member for remove and get content tests
			colMgr.members["test-col"] = []store.DocsetMember{
				{Path: "/readme.md", DocID: "doc-123"},
			}

			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.url, body)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			NewHandlerWithDocsets(&fakeAdapter{}, nil, colMgr).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.checkBody != nil {
				tt.checkBody(t, rec.Body.String())
			}
		})
	}
}

func TestHandlerDocsetRemoveMember(t *testing.T) {
	colMgr := newFakeDocsetManager()
	colMgr.docsets["test-col"] = store.Docset{Name: "test-col"}
	colMgr.members["test-col"] = []store.DocsetMember{
		{Path: "/readme.md", DocID: "doc-123"},
	}

	req := httptest.NewRequest(http.MethodDelete, "/v1/docsets/test-col/members?path=/readme.md", nil)
	rec := httptest.NewRecorder()

	NewHandlerWithDocsets(&fakeAdapter{}, nil, colMgr).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	// Verify member was removed
	if len(colMgr.members["test-col"]) != 0 {
		t.Errorf("members not removed, got %d members", len(colMgr.members["test-col"]))
	}
}

func TestHandlerDocsetErrors(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(colMgr *fakeDocsetManager)
		method     string
		url        string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name: "create duplicate name",
			setup: func(colMgr *fakeDocsetManager) {
				colMgr.createErr = store.ErrDocsetNameExists
			},
			method:     http.MethodPost,
			url:        "/v1/docsets",
			body:       `{"name":"existing"}`,
			wantStatus: http.StatusConflict,
			wantCode:   "DOCSET_NAME_EXISTS",
		},
		{
			name: "create invalid name",
			setup: func(colMgr *fakeDocsetManager) {
				colMgr.createErr = store.ErrInvalidName
			},
			method:     http.MethodPost,
			url:        "/v1/docsets",
			body:       `{"name":"Invalid-Name"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST", // ErrInvalidName is mapped to BAD_REQUEST
		},
		{
			name: "get not found",
			setup: func(colMgr *fakeDocsetManager) {
				colMgr.getErr = store.ErrDocsetNotFound
			},
			method:     http.MethodGet,
			url:        "/v1/docsets/nonexistent",
			wantStatus: http.StatusNotFound,
			wantCode:   "DOCSET_NOT_FOUND",
		},
		{
			name: "add member duplicate path",
			setup: func(colMgr *fakeDocsetManager) {
				colMgr.docsets["test-col"] = store.Docset{Name: "test-col"}
				colMgr.addMemberErr = store.ErrDocsetMemberExists
			},
			method:     http.MethodPut,
			url:        "/v1/docsets/test-col/members",
			body:       `{"source_ref":"repo://test/readme.md","path":"/readme.md"}`,
			wantStatus: http.StatusConflict,
			wantCode:   "DOCSET_MEMBER_EXISTS",
		},
		{
			name: "add member duplicate doc",
			setup: func(colMgr *fakeDocsetManager) {
				colMgr.docsets["test-col"] = store.Docset{Name: "test-col"}
				colMgr.addMemberErr = store.ErrDocAlreadyInDocset
			},
			method:     http.MethodPut,
			url:        "/v1/docsets/test-col/members",
			body:       `{"source_ref":"repo://test/readme.md","path":"/other.md"}`,
			wantStatus: http.StatusConflict,
			wantCode:   "DOC_ALREADY_IN_DOCSET",
		},
		{
			name: "get member content not found",
			setup: func(colMgr *fakeDocsetManager) {
				colMgr.docsets["test-col"] = store.Docset{Name: "test-col"}
				colMgr.getContentErr = store.ErrNotFound
			},
			method:     http.MethodGet,
			url:        "/v1/docsets/test-col/docs?path=/nonexistent.md",
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			colMgr := newFakeDocsetManager()
			if tt.setup != nil {
				tt.setup(colMgr)
			}

			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.url, body)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			NewHandlerWithDocsets(&fakeAdapter{}, nil, colMgr).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantCode) {
				t.Fatalf("body = %q, want code %q", rec.Body.String(), tt.wantCode)
			}
		})
	}
}

func TestHandlerDocsetAddMemberRequest(t *testing.T) {
	colMgr := newFakeDocsetManager()
	colMgr.docsets["test-col"] = store.Docset{Name: "test-col"}

	body := `{"source_ref":"repo://my-repo/docs/readme.md","path":"/docs/readme.md"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/docsets/test-col/members", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	NewHandlerWithDocsets(&fakeAdapter{}, nil, colMgr).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if colMgr.lastAddMemberReq.SourceRef != "repo://my-repo/docs/readme.md" {
		t.Errorf("source_ref = %q, want repo://my-repo/docs/readme.md", colMgr.lastAddMemberReq.SourceRef)
	}
	if colMgr.lastAddMemberReq.Path != "/docs/readme.md" {
		t.Errorf("path = %q, want /docs/readme.md", colMgr.lastAddMemberReq.Path)
	}
}
