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

	"github.com/austiecodes/gxfs/internal/store"
)

func TestClientLSBuildsURLAndDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repos/gxfs/ls" {
			t.Fatalf("path = %q, want /v1/repos/gxfs/ls", r.URL.Path)
		}
		if r.URL.Query().Get("path") != "/docs" {
			t.Fatalf("query path = %q, want /docs", r.URL.Query().Get("path"))
		}
		_ = json.NewEncoder(w).Encode(store.LSResponse{
			Nodes: []store.Node{{Path: "/docs/readme.md", Name: "readme.md", Kind: "file"}},
		})
	}))
	defer server.Close()

	resp, err := New(server.URL).LS(context.Background(), store.LSRequest{Repo: "gxfs", Path: "/docs"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Name != "readme.md" {
		t.Fatalf("LS() = %+v, want readme node", resp)
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
		if r.URL.Path != "/v1/repos/gxfs/grep" {
			t.Fatalf("path = %q, want grep endpoint", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("path") != "/" || q.Get("pattern") != "type Adapter" || q.Get("regex") != "true" {
			t.Fatalf("query = %s, want path/pattern/regex", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(store.GrepResponse{
			Matches: []store.Match{{Path: "/go/store.go", Line: 12, Text: "type Adapter interface {"}},
		})
	}))
	defer server.Close()

	resp, err := New(server.URL).Grep(context.Background(), store.GrepRequest{
		Repo: "gxfs", Path: "/", Pattern: "type Adapter", Regex: true,
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

	_, err := New(server.URL).Cat(context.Background(), store.CatRequest{Repo: "gxfs", Path: "/missing"})
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
		if r.URL.Path != "/v1/repos/gxfs/search" {
			t.Fatalf("path = %q, want /v1/repos/gxfs/search", r.URL.Path)
		}
		q := r.URL.Query()
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
		Repo:  "gxfs",
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
	resp, err := c.Cat(context.Background(), store.CatRequest{Repo: "gxfs", Path: "/docs/readme.md"})
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
		Repo:        "gxfs",
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
		Repo:        "gxfs",
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

	_, err := New(server.URL).Cat(context.Background(), store.CatRequest{Repo: "gxfs", Path: "/missing.md"})
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

	_, err := New(server.URL).Stat(context.Background(), store.StatRequest{Repo: "gxfs", Path: "/missing.md"})
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error = %q, want it to wrap store.ErrNotFound", err.Error())
	}
}
