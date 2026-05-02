package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gxfs/internal/store"
)

type fakeAdapter struct {
	lsReq   store.LSRequest
	grepReq store.GrepRequest
}

func (f *fakeAdapter) LS(_ context.Context, req store.LSRequest) (*store.LSResponse, error) {
	f.lsReq = req
	return &store.LSResponse{Nodes: []store.Node{{Path: "/docs", Name: "docs", Kind: "dir"}}}, nil
}

func (f *fakeAdapter) Tree(context.Context, store.TreeRequest) (*store.TreeResponse, error) {
	return &store.TreeResponse{Text: "/\n"}, nil
}

func (f *fakeAdapter) Cat(context.Context, store.CatRequest) (*store.CatResponse, error) {
	return &store.CatResponse{Path: "/docs/readme.md", Content: "# Readme\n"}, nil
}

func (f *fakeAdapter) Grep(_ context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	f.grepReq = req
	return &store.GrepResponse{Matches: []store.Match{{Path: "/go/store.go", Line: 12, Text: "type Adapter interface {"}}}, nil
}

func (f *fakeAdapter) Find(context.Context, store.FindRequest) (*store.FindResponse, error) {
	return &store.FindResponse{Nodes: []store.Node{{Path: "/go/store.go", Name: "store.go", Kind: "file"}}}, nil
}

func (f *fakeAdapter) Stat(context.Context, store.StatRequest) (*store.StatResponse, error) {
	return &store.StatResponse{Node: store.Node{Path: "/docs", Name: "docs", Kind: "dir"}}, nil
}

func TestHandlerRoutesLS(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/gxfs/ls?path=/docs", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter).ServeHTTP(rec, req)

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

func TestHandlerRoutesGrepRegex(t *testing.T) {
	adapter := &fakeAdapter{}
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/gxfs/grep?path=/go&pattern=Adapter&regex=true", nil)
	rec := httptest.NewRecorder()

	NewHandler(adapter).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if adapter.grepReq.Repo != "gxfs" || adapter.grepReq.Path != "/go" ||
		adapter.grepReq.Pattern != "Adapter" || !adapter.grepReq.Regex {
		t.Fatalf("grep req = %+v, want regex grep", adapter.grepReq)
	}
}

func TestHandlerHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeAdapter{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Fatalf("healthz = %d %q, want ok", rec.Code, rec.Body.String())
	}
}
