package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gxfs/internal/store"
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
