package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/austiecodes/rolio/internal/store"
)

func TestClientLSBuildsURLAndDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repos/ls" {
			t.Fatalf("path = %q, want /v1/repos/ls", r.URL.Path)
		}
		if r.URL.Query().Get("repo") != "rolio" {
			t.Fatalf("query repo = %q, want rolio", r.URL.Query().Get("repo"))
		}
		if r.URL.Query().Get("path") != "/docs" {
			t.Fatalf("query path = %q, want /docs", r.URL.Query().Get("path"))
		}
		_ = json.NewEncoder(w).Encode(store.LSResponse{
			Nodes: []store.Node{{Path: "/docs/readme.md", Name: "readme.md", Kind: "file"}},
		})
	}))
	defer server.Close()

	resp, err := New(server.URL).LS(context.Background(), store.LSRequest{Repo: "rolio", Path: "/docs"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Name != "readme.md" {
		t.Fatalf("LS() = %+v, want readme node", resp)
	}
}

func TestClientEscapesSourceNamesWithSlashesOnce(t *testing.T) {
	const repoName = "github.com/austiecodes/xxxx"

	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedPath := r.URL.EscapedPath()
		if escapedPath != "/v1/repos/ls" && escapedPath != "/v1/repos/tree" && escapedPath != "/v1/repos/write" {
			t.Fatalf("escaped path = %q, want repo operation endpoint", escapedPath)
		}
		if strings.Contains(r.URL.RawQuery, "%252F") {
			t.Fatalf("raw query = %q, contains double-encoded slash", r.URL.RawQuery)
		}
		if got := r.URL.Query().Get("repo"); got != repoName {
			t.Fatalf("query repo = %q, want %q; raw query = %q", got, repoName, r.URL.RawQuery)
		}
		op := strings.TrimPrefix(escapedPath, "/v1/repos/")
		seen[r.Method+" "+op] = true

		switch r.Method + " " + op {
		case http.MethodGet + " ls":
			_ = json.NewEncoder(w).Encode(store.LSResponse{
				Nodes: []store.Node{{Path: "/docs", Name: "docs", Kind: "dir"}},
			})
		case http.MethodGet + " tree":
			_ = json.NewEncoder(w).Encode(store.TreeResponse{Text: "/docs\n"})
		case http.MethodPut + " write":
			_ = json.NewEncoder(w).Encode(store.PutResponse{
				Node: store.Node{Path: r.URL.Query().Get("path"), Name: "guide.md", Kind: "file"},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, escapedPath)
		}
	}))
	defer server.Close()

	cli := New(server.URL)
	if _, err := cli.LS(context.Background(), store.LSRequest{Repo: repoName, Path: "/docs"}); err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if _, err := cli.Tree(context.Background(), store.TreeRequest{Repo: repoName, Path: "/docs"}); err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if _, err := cli.Put(context.Background(), store.PutRequest{Repo: repoName, Path: "/docs/guide.md", Content: "guide"}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	for _, want := range []string{
		http.MethodGet + " ls",
		http.MethodGet + " tree",
		http.MethodPut + " write",
	} {
		if !seen[want] {
			t.Fatalf("request %q was not observed", want)
		}
	}
}

func TestClientMountSourcesBuildsURLAndDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/mount-sources" {
			t.Fatalf("path = %q, want /v1/mount-sources", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string][]store.MountSource{
			"sources": {
				{Ref: "repo://rolio", Kind: store.SourceKindRepo, Name: "rolio"},
				{Ref: "repo://github%2Fopenai-go", Kind: store.SourceKindRepo, Name: "github/openai-go"},
			},
		})
	}))
	defer server.Close()

	sources, err := New(server.URL).MountSources(context.Background())
	if err != nil {
		t.Fatalf("MountSources() error = %v", err)
	}
	if len(sources) != 2 || sources[0].Ref != "repo://rolio" || sources[1].Name != "github/openai-go" {
		t.Fatalf("MountSources() = %+v, want decoded sources", sources)
	}
}

func TestClientRegisterRepoPostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/repos" {
			t.Fatalf("path = %q, want /v1/repos", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Name != "austiecodes/xxxx" {
			t.Fatalf("body name = %q, want austiecodes/xxxx", body.Name)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintln(w, `{"repo":{"name":"austiecodes/xxxx"}}`)
	}))
	defer server.Close()

	if err := New(server.URL).RegisterRepo(context.Background(), "austiecodes/xxxx"); err != nil {
		t.Fatalf("RegisterRepo() error = %v", err)
	}
}

func TestClientRecordUsageEventPostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/usage-events" {
			t.Fatalf("path = %q, want /v1/usage-events", r.URL.Path)
		}
		if got := r.Header.Get("X-Rolio-Log-Id"); got != "log-1" {
			t.Fatalf("X-Rolio-Log-Id = %q, want log-1", got)
		}
		var body store.UsageEvent
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Command != "search" || body.ClientRepo != "rolio" || body.DurationMs != 42 {
			t.Fatalf("body = %+v, want search usage event", body)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintln(w, `{"event":{"id":"usage-1","command":"search"}}`)
	}))
	defer server.Close()

	cli := New(server.URL)
	cli.SetLogID("log-1")
	resp, err := cli.RecordUsageEvent(context.Background(), store.UsageEvent{
		LogID:      "log-1",
		ClientRepo: "rolio",
		Command:    "search",
		DurationMs: 42,
	})
	if err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if resp.Event.ID != "usage-1" {
		t.Fatalf("response ID = %q, want usage-1", resp.Event.ID)
	}
}

func TestClientRegisterRepoDuplicateReturnsJSONMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintln(w, `{"error":{"code":"REPO_EXISTS","message":"repo already registered: austiecodes/xxxx"}}`)
	}))
	defer server.Close()

	err := New(server.URL).RegisterRepo(context.Background(), "austiecodes/xxxx")
	if err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
	if !errors.Is(err, store.ErrRepoExists) {
		t.Fatalf("error = %v, want ErrRepoExists", err)
	}
	if !strings.Contains(err.Error(), "repo already registered: austiecodes/xxxx") {
		t.Fatalf("error = %q, want duplicate message", err.Error())
	}
	if strings.Contains(err.Error(), `{"error"`) {
		t.Fatalf("error = %q, should not contain raw JSON", err.Error())
	}
}

func TestClientAdapterForSourceDocsBuildsURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/docs/cat" {
			t.Fatalf("path = %q, want /v1/docs/cat", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "openai-go-sdk" {
			t.Fatalf("query name = %q, want openai-go-sdk", r.URL.Query().Get("name"))
		}
		if r.URL.Query().Get("path") != "/usage.md" {
			t.Fatalf("query path = %q, want /usage.md", r.URL.Query().Get("path"))
		}
		_ = json.NewEncoder(w).Encode(store.CatResponse{Path: "/usage.md", Content: "usage"})
	}))
	defer server.Close()

	adapter, err := New(server.URL).AdapterForSource(context.Background(), store.SourceRef{
		Kind: store.SourceKindDocs,
		Name: "openai-go-sdk",
	})
	if err != nil {
		t.Fatalf("AdapterForSource(docs) error = %v", err)
	}

	resp, err := adapter.Cat(context.Background(), store.CatRequest{Path: "/usage.md"})
	if err != nil {
		t.Fatalf("Cat() error = %v", err)
	}
	if resp.Content != "usage" {
		t.Fatalf("Cat() content = %q, want usage", resp.Content)
	}
}

func TestClientAdapterForSourceRepoBuildsURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repos/stat" {
			t.Fatalf("path = %q, want /v1/repos/stat", r.URL.Path)
		}
		if r.URL.Query().Get("repo") != "rolio" {
			t.Fatalf("query repo = %q, want rolio", r.URL.Query().Get("repo"))
		}
		if r.URL.Query().Get("path") != "/docs" {
			t.Fatalf("query path = %q, want /docs", r.URL.Query().Get("path"))
		}
		_ = json.NewEncoder(w).Encode(store.StatResponse{
			Node: store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		})
	}))
	defer server.Close()

	adapter, err := New(server.URL).AdapterForSource(context.Background(), store.SourceRef{
		Kind: store.SourceKindRepo,
		Name: "rolio",
	})
	if err != nil {
		t.Fatalf("AdapterForSource(repo) error = %v", err)
	}

	resp, err := adapter.Stat(context.Background(), store.StatRequest{Path: "/docs"})
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if resp.Node.Path != "/docs" {
		t.Fatalf("Stat() path = %q, want /docs", resp.Node.Path)
	}
}

func TestClientAdapterForSourceDocsetBuildsURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/docset/tree" {
			t.Fatalf("path = %q, want /v1/docset/tree", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "best-practices" {
			t.Fatalf("query name = %q, want best-practices", r.URL.Query().Get("name"))
		}
		if r.URL.Query().Get("path") != "/go" {
			t.Fatalf("query path = %q, want /go", r.URL.Query().Get("path"))
		}
		_ = json.NewEncoder(w).Encode(store.TreeResponse{
			Root: store.Node{Path: "/go", Name: "go", Kind: "dir"},
			Text: "/go\n  errors.md\n",
		})
	}))
	defer server.Close()

	adapter, err := New(server.URL).AdapterForSource(context.Background(), store.SourceRef{
		Kind: store.SourceKindDocset,
		Name: "best-practices",
	})
	if err != nil {
		t.Fatalf("AdapterForSource(docset) error = %v", err)
	}

	resp, err := adapter.Tree(context.Background(), store.TreeRequest{Path: "/go"})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if resp.Root.Path != "/go" {
		t.Fatalf("Tree() root path = %q, want /go", resp.Root.Path)
	}
}

func TestClientLSParams(t *testing.T) {
	tests := []struct {
		name      string
		req       store.LSRequest
		wantParam string
	}{
		{
			name:      "sort",
			req:       store.LSRequest{Repo: "test", Path: "/", Sort: "size"},
			wantParam: "sort=size",
		},
		{
			name:      "reverse",
			req:       store.LSRequest{Repo: "test", Path: "/", Reverse: true},
			wantParam: "reverse=true",
		},
		{
			name:      "recursive",
			req:       store.LSRequest{Repo: "test", Path: "/", Recursive: true},
			wantParam: "recursive=true",
		},
		{
			name:      "all",
			req:       store.LSRequest{Repo: "test", Path: "/", All: true},
			wantParam: "all=true",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				_ = json.NewEncoder(w).Encode(store.LSResponse{})
			}))
			defer server.Close()

			_, err := New(server.URL).LS(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("LS() error = %v", err)
			}
			if !containsParam(gotQuery, tt.wantParam) {
				t.Fatalf("query = %q, want to contain %q", gotQuery, tt.wantParam)
			}
		})
	}
}

func TestClientLSZeroValuesOmitParams(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(store.LSResponse{})
	}))
	defer server.Close()

	_, err := New(server.URL).LS(context.Background(), store.LSRequest{Repo: "test", Path: "/"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	for _, param := range []string{"sort=", "reverse=", "recursive=", "all="} {
		if containsParam(gotQuery, param) {
			t.Fatalf("query = %q, should not contain %q", gotQuery, param)
		}
	}
}

func containsParam(query, param string) bool {
	return strings.Contains(query, param)
}

func TestClientGrepPassesPatternAndRegex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repos/grep" {
			t.Fatalf("path = %q, want grep endpoint", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("repo") != "rolio" || q.Get("path") != "/" || q.Get("pattern") != "type Adapter" || q.Get("regex") != "true" {
			t.Fatalf("query = %s, want path/pattern/regex", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(store.GrepResponse{
			Matches: []store.Match{{Path: "/go/store.go", Line: 12, Text: "type Adapter interface {"}},
		})
	}))
	defer server.Close()

	resp, err := New(server.URL).Grep(context.Background(), store.GrepRequest{
		Repo: "rolio", Path: "/", Pattern: "type Adapter", Regex: true,
	})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	if len(resp.Matches) != 1 || resp.Matches[0].Line != 12 {
		t.Fatalf("Grep() = %+v, want decoded match", resp)
	}
}

func TestClientGrepParams(t *testing.T) {
	tests := []struct {
		name      string
		req       store.GrepRequest
		wantParam string
	}{
		{
			name:      "case_insensitive",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", CaseInsensitive: true},
			wantParam: "case_insensitive=true",
		},
		{
			name:      "invert",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", Invert: true},
			wantParam: "invert=true",
		},
		{
			name:      "whole_word",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", WholeWord: true},
			wantParam: "whole_word=true",
		},
		{
			name:      "whole_line",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", WholeLine: true},
			wantParam: "whole_line=true",
		},
		{
			name:      "context_before",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", ContextBefore: 3},
			wantParam: "context_before=3",
		},
		{
			name:      "context_after",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", ContextAfter: 5},
			wantParam: "context_after=5",
		},
		{
			name:      "all",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", All: true},
			wantParam: "all=true",
		},
		{
			name:      "include",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", Include: "*.go"},
			wantParam: "include=%2A.go",
		},
		{
			name:      "exclude",
			req:       store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello", Exclude: "*.md"},
			wantParam: "exclude=%2A.md",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				_ = json.NewEncoder(w).Encode(store.GrepResponse{})
			}))
			defer server.Close()

			_, err := New(server.URL).Grep(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Grep() error = %v", err)
			}
			if !containsParam(gotQuery, tt.wantParam) {
				t.Fatalf("query = %q, want to contain %q", gotQuery, tt.wantParam)
			}
		})
	}
}

func TestClientGrepZeroValuesOmitParams(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(store.GrepResponse{})
	}))
	defer server.Close()

	_, err := New(server.URL).Grep(context.Background(), store.GrepRequest{Repo: "test", Path: "/", Pattern: "hello"})
	if err != nil {
		t.Fatalf("Grep() error = %v", err)
	}
	for _, param := range []string{
		"regex=", "case_insensitive=", "invert=", "whole_word=", "whole_line=",
		"context_before=", "context_after=", "all=", "include=", "exclude=",
	} {
		if containsParam(gotQuery, param) {
			t.Fatalf("query = %q, should not contain %q", gotQuery, param)
		}
	}
}

func TestClientReturnsHTTPErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "path not found", http.StatusNotFound)
	}))
	defer server.Close()

	_, err := New(server.URL).Cat(context.Background(), store.CatRequest{Repo: "rolio", Path: "/missing"})
	if err == nil {
		t.Fatal("Cat() error = nil, want HTTP error")
	}
}

func TestClientFindParams(t *testing.T) {
	tests := []struct {
		name      string
		req       store.FindRequest
		wantParam string
	}{
		{
			name:      "type",
			req:       store.FindRequest{Repo: "test", Path: "/", Type: "dir"},
			wantParam: "type=dir",
		},
		{
			name:      "maxdepth",
			req:       store.FindRequest{Repo: "test", Path: "/", MaxDepth: 5},
			wantParam: "maxdepth=5",
		},
		{
			name:      "mindepth",
			req:       store.FindRequest{Repo: "test", Path: "/", MinDepth: 2},
			wantParam: "mindepth=2",
		},
		{
			name:      "all",
			req:       store.FindRequest{Repo: "test", Path: "/", All: true},
			wantParam: "all=true",
		},
		{
			name:      "iname",
			req:       store.FindRequest{Repo: "test", Path: "/", IName: "README"},
			wantParam: "iname=README",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				_ = json.NewEncoder(w).Encode(store.FindResponse{})
			}))
			defer server.Close()

			_, err := New(server.URL).Find(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			if !containsParam(gotQuery, tt.wantParam) {
				t.Fatalf("query = %q, want to contain %q", gotQuery, tt.wantParam)
			}
		})
	}
}

func TestClientFindZeroValuesOmitParams(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(store.FindResponse{})
	}))
	defer server.Close()

	_, err := New(server.URL).Find(context.Background(), store.FindRequest{Repo: "test", Path: "/"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	for _, param := range []string{"name=", "type=", "maxdepth=", "mindepth=", "all=", "iname="} {
		if containsParam(gotQuery, param) {
			t.Fatalf("query = %q, should not contain %q", gotQuery, param)
		}
	}
}

func TestClientTreeParams(t *testing.T) {
	tests := []struct {
		name      string
		req       store.TreeRequest
		wantParam string
	}{
		{
			name:      "all",
			req:       store.TreeRequest{Repo: "test", Path: "/", All: true},
			wantParam: "all=true",
		},
		{
			name:      "dirs_only",
			req:       store.TreeRequest{Repo: "test", Path: "/", DirsOnly: true},
			wantParam: "dirs_only=true",
		},
		{
			name:      "full_path",
			req:       store.TreeRequest{Repo: "test", Path: "/", FullPath: true},
			wantParam: "full_path=true",
		},
		{
			name:      "show_size",
			req:       store.TreeRequest{Repo: "test", Path: "/", ShowSize: true},
			wantParam: "show_size=true",
		},
		{
			name:      "sort",
			req:       store.TreeRequest{Repo: "test", Path: "/", Sort: "size"},
			wantParam: "sort=size",
		},
		{
			name:      "dirs_first",
			req:       store.TreeRequest{Repo: "test", Path: "/", DirsFirst: true},
			wantParam: "dirs_first=true",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				_ = json.NewEncoder(w).Encode(store.TreeResponse{})
			}))
			defer server.Close()

			_, err := New(server.URL).Tree(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Tree() error = %v", err)
			}
			if !containsParam(gotQuery, tt.wantParam) {
				t.Fatalf("query = %q, want to contain %q", gotQuery, tt.wantParam)
			}
		})
	}
}

func TestClientTreeZeroValuesOmitParams(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(store.TreeResponse{})
	}))
	defer server.Close()

	_, err := New(server.URL).Tree(context.Background(), store.TreeRequest{Repo: "test", Path: "/"})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	for _, param := range []string{"all=", "dirs_only=", "full_path=", "show_size=", "sort=", "dirs_first="} {
		if containsParam(gotQuery, param) {
			t.Fatalf("query = %q, should not contain %q", gotQuery, param)
		}
	}
}

func TestClientSearchParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repos/search" {
			t.Fatalf("path = %q, want /v1/repos/search", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("repo") != "rolio" {
			t.Fatalf("query repo = %q, want rolio", q.Get("repo"))
		}
		if q.Get("q") != "openai-go" {
			t.Fatalf("query q = %q, want openai-go", q.Get("q"))
		}
		if q.Get("path") != "/docs" {
			t.Fatalf("query path = %q, want /docs", q.Get("path"))
		}
		if q.Get("limit") != "10" {
			t.Fatalf("query limit = %q, want 10", q.Get("limit"))
		}
		_ = json.NewEncoder(w).Encode(store.SearchResponse{
			Results: []store.SearchResult{{Path: "/docs/gotchas.md", Rank: 0.95, Snippet: "**openai-go** issue"}},
			Total:   1,
		})
	}))
	defer server.Close()

	resp, err := New(server.URL).Search(context.Background(), store.SearchRequest{
		Repo:  "rolio",
		Query: "openai-go",
		Path:  "/docs",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if resp.Total != 1 || len(resp.Results) != 1 {
		t.Fatalf("Search() = %+v, want 1 result", resp)
	}
	if resp.Results[0].Path != "/docs/gotchas.md" {
		t.Fatalf("result path = %q, want /docs/gotchas.md", resp.Results[0].Path)
	}
}

func TestClientCatWithoutIfNoneMatch(t *testing.T) {
	var gotIfNoneMatch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"path":"/docs/readme.md","content":"hello","hash":"sha256:abc"}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	resp, err := c.Cat(context.Background(), store.CatRequest{Repo: "rolio", Path: "/docs/readme.md"})
	if err != nil {
		t.Fatalf("Cat error: %v", err)
	}
	if gotIfNoneMatch != "" {
		t.Fatalf("If-None-Match = %q, want empty (no known hash)", gotIfNoneMatch)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q, want hello", resp.Content)
	}
	if resp.Hash != "sha256:abc" {
		t.Fatalf("hash = %q, want sha256:abc", resp.Hash)
	}
}

func TestClientCatWithIfNoneMatchSendsHeader(t *testing.T) {
	var gotIfNoneMatch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"path":"/docs/readme.md","content":"hello","hash":"sha256:abc"}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Cat(context.Background(), store.CatRequest{
		Repo:        "rolio",
		Path:        "/docs/readme.md",
		IfNoneMatch: "sha256:abc",
	})
	if err != nil {
		t.Fatalf("Cat error: %v", err)
	}
	want := `"sha256:abc"`
	if gotIfNoneMatch != want {
		t.Fatalf("If-None-Match = %q, want %q", gotIfNoneMatch, want)
	}
}

func TestClientCat304ReturnsErrNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"sha256:abc"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	c := New(srv.URL)
	resp, err := c.Cat(context.Background(), store.CatRequest{
		Repo:        "rolio",
		Path:        "/docs/readme.md",
		IfNoneMatch: "sha256:abc",
	})
	if err == nil {
		t.Fatal("expected error for 304, got nil")
	}
	if !errors.Is(err, store.ErrNotModified) {
		t.Fatalf("error = %v, want ErrNotModified", err)
	}
	if resp == nil {
		t.Fatal("resp should not be nil on 304")
	}
	if resp.Hash != "sha256:abc" {
		t.Fatalf("304 resp hash = %q, want sha256:abc", resp.Hash)
	}
}

func TestClientCatErrorParsesJSONMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintln(w, `{"error":{"code":"NOT_FOUND","message":"path not found: /missing.md"}}`)
	}))
	defer server.Close()

	_, err := New(server.URL).Cat(context.Background(), store.CatRequest{Repo: "rolio", Path: "/missing.md"})
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	// Must contain the parsed message, not raw JSON
	if !strings.Contains(err.Error(), "path not found: /missing.md") {
		t.Fatalf("error = %q, want parsed message", err.Error())
	}
	if strings.Contains(err.Error(), `{"error"`) {
		t.Fatalf("error = %q, should not contain raw JSON (regression: Cat must use shared error parsing)", err.Error())
	}
}

func TestClient404MappedToErrNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "NOT_FOUND", "message": "path not found: /missing.md"},
		})
	}))
	defer server.Close()

	_, err := New(server.URL).Stat(context.Background(), store.StatRequest{Repo: "rolio", Path: "/missing.md"})
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error = %q, want it to wrap store.ErrNotFound", err.Error())
	}
}
