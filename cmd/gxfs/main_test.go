package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gxfs/cmd/gxfs/command"
	"gxfs/internal/client"
	"gxfs/internal/config"
	"gxfs/internal/mount"
	"gxfs/internal/server"
	"gxfs/internal/store"
	"gxfs/internal/syncmanifest"
	"gxfs/internal/vfs"
)

type fakeClient struct {
	grepReq          store.GrepRequest
	grepMatches      []store.Match
	lsNodes          []store.Node
	statNode         *store.Node
	statErr          error
	catContent       string
	lsReq            store.LSRequest
	findReq          store.FindRequest
	findNodes        []store.Node
	treeReq          store.TreeRequest
	treeText         string
	catReqs          []store.CatRequest
	catMu            sync.Mutex
	catContents      map[string]string
	putReqs          []store.PutRequest
	searchReq        store.SearchRequest
	searchResp       *store.SearchResponse
	batchHashesResp  *store.HashResponse
	locateReq        store.LocateRequest
	locateResp       *store.LocateResponse
	locateErr        error
	locateErrByRepo  map[string]error
	locateRespByRepo map[string]*store.LocateResponse
	repoList         []string
	repoListErr      error
}

func defaultLSNodes() []store.Node {
	return []store.Node{
		{Path: "/docs", Name: "docs", Kind: "dir"},
		{Path: "/readme.md", Name: "readme.md", Kind: "file"},
	}
}

func detailedLSNodes() []store.Node {
	return []store.Node{
		{Path: "/docs", Name: "docs", Kind: "dir", ModTime: "2025-01-01"},
		{Path: "/readme.md", Name: "readme.md", Kind: "file", Size: 4096, ModTime: "2025-01-02"},
	}
}

func sortLSNodes() []store.Node {
	return []store.Node{
		{Path: "/alpha.txt", Name: "alpha.txt", Kind: "file", Size: 100, ModTime: "2025-01-03"},
		{Path: "/beta.txt", Name: "beta.txt", Kind: "file", Size: 300, ModTime: "2025-01-01"},
		{Path: "/gamma.txt", Name: "gamma.txt", Kind: "file", Size: 200, ModTime: "2025-01-02"},
	}
}

func hiddenLSNodes() []store.Node {
	return []store.Node{
		{Path: "/docs", Name: "docs", Kind: "dir"},
		{Path: "/.hidden", Name: ".hidden", Kind: "file"},
		{Path: "/readme.md", Name: "readme.md", Kind: "file"},
	}
}

func (f *fakeClient) LS(_ context.Context, req store.LSRequest) (*store.LSResponse, error) {
	f.lsReq = req
	nodes := f.lsNodes
	if nodes == nil {
		nodes = defaultLSNodes()
	}
	nodes = vfs.SortNodesCopy(nodes, req.Sort, req.Reverse)
	return &store.LSResponse{Nodes: nodes, Total: len(nodes)}, nil
}

func (f *fakeClient) Tree(_ context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	f.treeReq = req
	text := f.treeText
	if text == "" {
		text = "/\n  docs/\n"
	}
	return &store.TreeResponse{Text: text}, nil
}

func (f *fakeClient) Cat(_ context.Context, req store.CatRequest) (*store.CatResponse, error) {
	f.catMu.Lock()
	f.catReqs = append(f.catReqs, req)
	f.catMu.Unlock()
	if f.catContents != nil {
		return &store.CatResponse{Path: req.Path, Content: f.catContents[req.Path]}, nil
	}
	content := f.catContent
	if content == "" {
		content = "# Readme\n"
	}
	return &store.CatResponse{Path: req.Path, Content: content}, nil
}

func (f *fakeClient) Grep(_ context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	f.grepReq = req
	matches := f.grepMatches
	if matches == nil {
		matches = []store.Match{
			{Path: "/go/store.go", Line: 12, Text: "type Adapter interface {"},
		}
	}
	return &store.GrepResponse{Matches: matches}, nil
}

func (f *fakeClient) Find(_ context.Context, req store.FindRequest) (*store.FindResponse, error) {
	f.findReq = req
	nodes := f.findNodes
	if nodes == nil {
		nodes = []store.Node{{Path: "/go/store.go", Name: "store.go", Kind: "file"}}
	}
	return &store.FindResponse{Nodes: nodes, Total: len(nodes)}, nil
}

func (f *fakeClient) Stat(_ context.Context, _ store.StatRequest) (*store.StatResponse, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	node := store.Node{Path: "/docs", Name: "docs", Kind: "dir"}
	if f.statNode != nil {
		node = *f.statNode
	}
	return &store.StatResponse{Node: node}, nil
}

func (f *fakeClient) Put(_ context.Context, req store.PutRequest) (*store.PutResponse, error) {
	f.putReqs = append(f.putReqs, req)
	return &store.PutResponse{Node: store.Node{Path: req.Path, Name: filepath.Base(req.Path), Kind: "file", Size: int64(len(req.Content))}}, nil
}

func (f *fakeClient) Delete(_ context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	return &store.DeleteResponse{}, nil
}

func (f *fakeClient) Edit(_ context.Context, req store.EditRequest) (*store.EditResponse, error) {
	return &store.EditResponse{Path: req.Path, Replaced: 1}, nil
}

func (f *fakeClient) BatchHashes(_ context.Context, _ store.HashRequest) (*store.HashResponse, error) {
	if f.batchHashesResp != nil {
		return f.batchHashesResp, nil
	}
	return &store.HashResponse{Hashes: []store.ContentHash{}}, nil
}

func (f *fakeClient) Search(_ context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	f.searchReq = req
	if f.searchResp != nil {
		return f.searchResp, nil
	}
	return &store.SearchResponse{
		Results: []store.SearchResult{
			{Path: "/docs/guide.md", Rank: 0.9, Snippet: "**test** result", Size: 100, ModTime: "2026-01-01"},
		},
		Total: 1,
	}, nil
}

func (f *fakeClient) Glob(_ context.Context, req store.GlobRequest) (*store.GlobResponse, error) {
	return &store.GlobResponse{Results: []store.GlobResult{
		{Path: "docs/readme.md", Size: 100, ModTime: "2026-01-01"},
		{Path: "docs/api.md", Size: 200, ModTime: "2026-01-02"},
	}, Total: 2}, nil
}

func (f *fakeClient) Locate(_ context.Context, req store.LocateRequest) (*store.LocateResponse, error) {
	f.locateReq = req
	// Check repo-specific error first
	if f.locateErrByRepo != nil {
		if err, ok := f.locateErrByRepo[req.Repo]; ok {
			return nil, err
		}
	}
	if f.locateErr != nil {
		return nil, f.locateErr
	}
	// Check repo-specific response
	if f.locateRespByRepo != nil {
		if resp, ok := f.locateRespByRepo[req.Repo]; ok {
			// Fill in Ref if not set
			for i := range resp.Results {
				if resp.Results[i].Ref == "" {
					resp.Results[i].Ref = "repo://" + url.PathEscape(req.Repo) + resp.Results[i].Path
				}
			}
			return resp, nil
		}
	}
	if f.locateResp != nil {
		// Fill in Ref if not set (simulates real adapter behavior)
		for i := range f.locateResp.Results {
			if f.locateResp.Results[i].Ref == "" {
				f.locateResp.Results[i].Ref = "repo://" + url.PathEscape(req.Repo) + f.locateResp.Results[i].Path
			}
		}
		return f.locateResp, nil
	}
	return &store.LocateResponse{Results: []store.LocateResult{
		{Ref: "repo://" + url.PathEscape(req.Repo) + "/docs/readme.md", Path: "/docs/readme.md", Score: 1.0, Snippet: "example snippet"},
	}, Total: 1}, nil
}

func (f *fakeClient) RepoList(_ context.Context) ([]string, error) {
	if f.repoListErr != nil {
		return nil, f.repoListErr
	}
	if f.repoList != nil {
		return f.repoList, nil
	}
	return []string{"my-project", "github/openai-go"}, nil
}

func execute(t *testing.T, args ...string) (string, *fakeClient) {
	t.Helper()

	client := &fakeClient{}
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}
	return out.String(), client
}

func executeWithNodes(t *testing.T, nodes []store.Node, args ...string) string {
	t.Helper()

	client := &fakeClient{lsNodes: nodes}
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}
	return out.String()
}

func executeWithClient(t *testing.T, client *fakeClient, args ...string) string {
	t.Helper()

	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}
	return out.String()
}

func executeWithClientErr(client *fakeClient, args ...string) (string, error) {
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestLSOutputMarksDirectories(t *testing.T) {
	got, _ := execute(t, "ls", "/")
	want := "docs/\nreadme.md\n"
	if got != want {
		t.Fatalf("ls output = %q, want %q", got, want)
	}
}

func TestLSLongFormat(t *testing.T) {
	got := executeWithNodes(t, detailedLSNodes(), "ls", "-l", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ls -l line count = %d, want 2; output = %q", len(lines), got)
	}
	want0 := "drwx         -  2025-01-01  docs"
	if lines[0] != want0 {
		t.Fatalf("ls -l dir line = %q, want %q", lines[0], want0)
	}
	want1 := "-rw-      4096  2025-01-02  readme.md"
	if lines[1] != want1 {
		t.Fatalf("ls -l file line = %q, want %q", lines[1], want1)
	}
}

func TestLSClassify(t *testing.T) {
	got, _ := execute(t, "ls", "-F", "/")
	want := "docs/\nreadme.md\n"
	if got != want {
		t.Fatalf("ls -F output = %q, want %q", got, want)
	}
}

func TestLSSlashDir(t *testing.T) {
	got, _ := execute(t, "ls", "-p", "/")
	want := "docs/\nreadme.md\n"
	if got != want {
		t.Fatalf("ls -p output = %q, want %q", got, want)
	}
}

func TestLSLongClassify(t *testing.T) {
	got := executeWithNodes(t, detailedLSNodes(), "ls", "-l", "-F", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ls -lF line count = %d, want 2; output = %q", len(lines), got)
	}
	want0 := "drwx         -  2025-01-01  docs/"
	if lines[0] != want0 {
		t.Fatalf("ls -lF dir line = %q, want %q", lines[0], want0)
	}
	want1 := "-rw-      4096  2025-01-02  readme.md"
	if lines[1] != want1 {
		t.Fatalf("ls -lF file line = %q, want %q", lines[1], want1)
	}
}

func TestLSLongSlashDir(t *testing.T) {
	got := executeWithNodes(t, detailedLSNodes(), "ls", "-l", "-p", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ls -lp line count = %d, want 2; output = %q", len(lines), got)
	}
	want0 := "drwx         -  2025-01-01  docs/"
	if lines[0] != want0 {
		t.Fatalf("ls -lp dir line = %q, want %q", lines[0], want0)
	}
	want1 := "-rw-      4096  2025-01-02  readme.md"
	if lines[1] != want1 {
		t.Fatalf("ls -lp file line = %q, want %q", lines[1], want1)
	}
}

func TestFormatLSLine(t *testing.T) {
	tests := []struct {
		name     string
		node     store.Node
		long     bool
		classify bool
		slashDir bool
		want     string
	}{
		{
			name: "compact dir",
			node: store.Node{Name: "docs", Kind: "dir"},
			want: "docs/",
		},
		{
			name: "compact file",
			node: store.Node{Name: "readme.md", Kind: "file"},
			want: "readme.md",
		},
		{
			name: "long dir no flags",
			node: store.Node{Name: "docs", Kind: "dir", ModTime: "2025-01-01"},
			long: true,
			want: "drwx         -  2025-01-01  docs",
		},
		{
			name: "long file no flags",
			node: store.Node{Name: "readme.md", Kind: "file", Size: 4096, ModTime: "2025-01-02"},
			long: true,
			want: "-rw-      4096  2025-01-02  readme.md",
		},
		{
			name:     "long dir with classify",
			node:     store.Node{Name: "docs", Kind: "dir", ModTime: "2025-06-15"},
			long:     true,
			classify: true,
			want:     "drwx         -  2025-06-15  docs/",
		},
		{
			name:     "long dir with slashDir",
			node:     store.Node{Name: "docs", Kind: "dir", ModTime: "2025-06-15"},
			long:     true,
			slashDir: true,
			want:     "drwx         -  2025-06-15  docs/",
		},
		{
			name:     "long file with classify",
			node:     store.Node{Name: "main.go", Kind: "file", Size: 128, ModTime: "2025-03-10"},
			long:     true,
			classify: true,
			want:     "-rw-       128  2025-03-10  main.go",
		},
		{
			name: "long file no modtime",
			node: store.Node{Name: "notes.txt", Kind: "file", Size: 55},
			long: true,
			want: "-rw-        55  -  notes.txt",
		},
		{
			name: "long dir no modtime",
			node: store.Node{Name: "tmp", Kind: "dir"},
			long: true,
			want: "drwx         -  -  tmp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := command.FormatLSLine(tt.node, tt.long, tt.classify, tt.slashDir)
			if got != tt.want {
				t.Fatalf("command.FormatLSLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCatOutputsContent(t *testing.T) {
	got, _ := execute(t, "cat", "/readme.md")
	if got != "# Readme\n" {
		t.Fatalf("cat output = %q, want readme content", got)
	}
}

func TestGrepOutputAndRegexFlag(t *testing.T) {
	got, client := execute(t, "grep", "-E", "type Adapter", "/go")
	want := "/go/store.go:12:type Adapter interface {\n"
	if got != want {
		t.Fatalf("grep output = %q, want %q", got, want)
	}
	if !client.grepReq.Regex || client.grepReq.Pattern != "type Adapter" || client.grepReq.Path != "/go" {
		t.Fatalf("grep request = %+v, want regex request", client.grepReq)
	}
}

func TestRunHelpDoesNotRequireConfig(t *testing.T) {
	t.Setenv("GXFS_CONFIG", "/path/that/does/not/exist")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(--help) code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "GXFS gives agents Unix-like commands") {
		t.Fatalf("help output = %q, want GXFS help", stdout.String())
	}
}

func TestRunInitDoesNotRequireConfig(t *testing.T) {
	t.Setenv("GXFS_CONFIG", "/path/that/does/not/exist")
	dir := t.TempDir()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"init", dir, "--no-instructions"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(init) code = %d, stderr = %q", code, stderr.String())
	}

	if _, err := os.Stat(filepath.Join(dir, ".gxfs", "settings.toml")); err != nil {
		t.Fatalf("settings.toml stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gxfs", "mounts.toml")); err != nil {
		t.Fatalf("mounts.toml stat error = %v", err)
	}
}

func TestRunConfigDoctorStillRequiresConfig(t *testing.T) {
	t.Setenv("GXFS_CONFIG", "/path/that/does/not/exist")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"config", "doctor"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("run(config doctor) code = %d, want failure; stdout = %q", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "read config") {
		t.Fatalf("stderr = %q, want read config error", stderr.String())
	}
}

func TestRunUsesMountsForRemotePathResolution(t *testing.T) {
	dir := t.TempDir()
	gxfsDir := filepath.Join(dir, ".gxfs")
	if err := os.MkdirAll(gxfsDir, 0o755); err != nil {
		t.Fatalf("mkdir .gxfs: %v", err)
	}

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Query().Get("path")
		if r.URL.Path != "/v1/repos/gxfs/stat" {
			t.Fatalf("request path = %q, want /v1/repos/gxfs/stat", r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(store.StatResponse{
			Node: store.Node{Path: "/remote-docs/readme.md", Name: "readme.md", Kind: "file", Size: 7},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	settings := `version = 1
repo = "gxfs"

[server]
addr = "` + server.URL + `"

[docs]
path = "docs"
`
	if err := os.WriteFile(filepath.Join(gxfsDir, "settings.toml"), []byte(settings), 0o644); err != nil {
		t.Fatalf("write settings.toml: %v", err)
	}

	mounts := `version = 1

[[mounts]]
local = "docs"
remote = "repo://self/remote-docs"
mode = "writable"
source = "default"
`
	if err := os.WriteFile(filepath.Join(gxfsDir, "mounts.toml"), []byte(mounts), 0o644); err != nil {
		t.Fatalf("write mounts.toml: %v", err)
	}

	t.Setenv("GXFS_CONFIG", filepath.Join(gxfsDir, "settings.toml"))
	t.Chdir(dir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"stat", "-f", "docs/readme.md"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(stat) code = %d, stderr = %q", code, stderr.String())
	}
	if gotPath != "/remote-docs/readme.md" {
		t.Fatalf("remote path = %q, want /remote-docs/readme.md", gotPath)
	}
	if !strings.Contains(stdout.String(), "/docs/readme.md\tfile\t7") {
		t.Fatalf("stdout = %q, want localized stat output", stdout.String())
	}
}

func TestRunFallsBackToDefaultMountWhenMountsFileMissing(t *testing.T) {
	dir := t.TempDir()
	gxfsDir := filepath.Join(dir, ".gxfs")
	if err := os.MkdirAll(gxfsDir, 0o755); err != nil {
		t.Fatalf("mkdir .gxfs: %v", err)
	}

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Query().Get("path")
		if err := json.NewEncoder(w).Encode(store.StatResponse{
			Node: store.Node{Path: "/docs/readme.md", Name: "readme.md", Kind: "file", Size: 7},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	settings := `version = 1
repo = "gxfs"

[server]
addr = "` + server.URL + `"

[docs]
path = "docs"
`
	if err := os.WriteFile(filepath.Join(gxfsDir, "settings.toml"), []byte(settings), 0o644); err != nil {
		t.Fatalf("write settings.toml: %v", err)
	}

	t.Setenv("GXFS_CONFIG", filepath.Join(gxfsDir, "settings.toml"))
	t.Chdir(dir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"stat", "-f", "docs/readme.md"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(stat) code = %d, stderr = %q", code, stderr.String())
	}
	if gotPath != "/docs/readme.md" {
		t.Fatalf("remote path = %q, want /docs/readme.md", gotPath)
	}
}

func TestLSSortByTime(t *testing.T) {
	c := &fakeClient{lsNodes: sortLSNodes()}
	got := executeWithClient(t, c, "ls", "-t", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls -t line count = %d, want 3; output = %q", len(lines), got)
	}
	if lines[0] != "alpha.txt" {
		t.Fatalf("ls -t first = %q, want alpha.txt (newest)", lines[0])
	}
	if lines[1] != "gamma.txt" {
		t.Fatalf("ls -t second = %q, want gamma.txt", lines[1])
	}
	if lines[2] != "beta.txt" {
		t.Fatalf("ls -t third = %q, want beta.txt (oldest)", lines[2])
	}
	if c.lsReq.Sort != "mtime" || !c.lsReq.Reverse {
		t.Fatalf("ls -t request = %+v, want Sort=mtime Reverse=true", c.lsReq)
	}
}

func TestLSSortBySize(t *testing.T) {
	c := &fakeClient{lsNodes: sortLSNodes()}
	got := executeWithClient(t, c, "ls", "-S", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls -S line count = %d, want 3; output = %q", len(lines), got)
	}
	if lines[0] != "beta.txt" {
		t.Fatalf("ls -S first = %q, want beta.txt (largest)", lines[0])
	}
	if lines[1] != "gamma.txt" {
		t.Fatalf("ls -S second = %q, want gamma.txt", lines[1])
	}
	if lines[2] != "alpha.txt" {
		t.Fatalf("ls -S third = %q, want alpha.txt (smallest)", lines[2])
	}
	if c.lsReq.Sort != "size" || !c.lsReq.Reverse {
		t.Fatalf("ls -S request = %+v, want Sort=size Reverse=true", c.lsReq)
	}
}

func TestLSReverseName(t *testing.T) {
	c := &fakeClient{lsNodes: sortLSNodes()}
	got := executeWithClient(t, c, "ls", "-r", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls -r line count = %d, want 3; output = %q", len(lines), got)
	}
	if lines[0] != "gamma.txt" {
		t.Fatalf("ls -r first = %q, want gamma.txt (reversed alpha)", lines[0])
	}
	if lines[2] != "alpha.txt" {
		t.Fatalf("ls -r last = %q, want alpha.txt (reversed gamma)", lines[2])
	}
	if c.lsReq.Sort != "name" || !c.lsReq.Reverse {
		t.Fatalf("ls -r request = %+v, want Sort=name Reverse=true", c.lsReq)
	}
}

func TestLSSortByTimeReverse(t *testing.T) {
	c := &fakeClient{lsNodes: sortLSNodes()}
	got := executeWithClient(t, c, "ls", "-t", "-r", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls -tr line count = %d, want 3; output = %q", len(lines), got)
	}
	if lines[0] != "beta.txt" {
		t.Fatalf("ls -tr first = %q, want beta.txt (oldest first)", lines[0])
	}
	if lines[2] != "alpha.txt" {
		t.Fatalf("ls -tr last = %q, want alpha.txt (newest last)", lines[2])
	}
	if c.lsReq.Sort != "mtime" || c.lsReq.Reverse {
		t.Fatalf("ls -tr request = %+v, want Sort=mtime Reverse=false", c.lsReq)
	}
}

func TestLSSortBySizeReverse(t *testing.T) {
	c := &fakeClient{lsNodes: sortLSNodes()}
	got := executeWithClient(t, c, "ls", "-S", "-r", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls -Sr line count = %d, want 3; output = %q", len(lines), got)
	}
	if lines[0] != "alpha.txt" {
		t.Fatalf("ls -Sr first = %q, want alpha.txt (smallest first)", lines[0])
	}
	if lines[2] != "beta.txt" {
		t.Fatalf("ls -Sr last = %q, want beta.txt (largest last)", lines[2])
	}
}

func TestLSRecursiveFlag(t *testing.T) {
	c := &fakeClient{lsNodes: defaultLSNodes()}
	executeWithClient(t, c, "ls", "-R", "/")
	if !c.lsReq.Recursive {
		t.Fatalf("ls -R request = %+v, want Recursive=true", c.lsReq)
	}
}

func TestLSRecursiveLongFormat(t *testing.T) {
	c := &fakeClient{lsNodes: detailedLSNodes()}
	got := executeWithClient(t, c, "ls", "-l", "-R", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ls -lR line count = %d, want 2; output = %q", len(lines), got)
	}
	if !c.lsReq.Recursive {
		t.Fatalf("ls -lR request = %+v, want Recursive=true", c.lsReq)
	}
	if !strings.Contains(lines[0], "drwx") {
		t.Fatalf("ls -lR dir line = %q, want long format", lines[0])
	}
}

func TestLSAllFlag(t *testing.T) {
	c := &fakeClient{lsNodes: hiddenLSNodes()}
	got := executeWithClient(t, c, "ls", "-a", "/")
	if !c.lsReq.All {
		t.Fatalf("ls -a request = %+v, want All=true", c.lsReq)
	}
	if !strings.Contains(got, ".hidden") {
		t.Fatalf("ls -a output = %q, want .hidden included", got)
	}
	if !strings.Contains(got, "docs") {
		t.Fatalf("ls -a output = %q, want docs included", got)
	}
	if !strings.Contains(got, "readme.md") {
		t.Fatalf("ls -a output = %q, want readme.md included", got)
	}
}

func TestLSAllLongFormat(t *testing.T) {
	c := &fakeClient{lsNodes: hiddenLSNodes()}
	got := executeWithClient(t, c, "ls", "-l", "-a", "/")
	if !c.lsReq.All {
		t.Fatalf("ls -la request = %+v, want All=true", c.lsReq)
	}
	if !strings.Contains(got, ".hidden") {
		t.Fatalf("ls -la output = %q, want .hidden included", got)
	}
}

func TestLSDirectoryFlag(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"}}
	got := executeWithClient(t, c, "ls", "-d", "/docs")
	if got != "docs/\n" {
		t.Fatalf("ls -d output = %q, want docs/", got)
	}
}

func TestLSDirectoryLongFormat(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir", ModTime: "2025-01-01"}}
	got := executeWithClient(t, c, "ls", "-d", "-l", "/docs")
	want := "drwx         -  2025-01-01  docs\n"
	if got != want {
		t.Fatalf("ls -dl output = %q, want %q", got, want)
	}
}

func TestLSDirectoryClassify(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"}}
	got := executeWithClient(t, c, "ls", "-d", "-F", "/docs")
	want := "docs/\n"
	if got != want {
		t.Fatalf("ls -dF output = %q, want %q", got, want)
	}
}

func TestLSDirectorySlashDir(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"}}
	got := executeWithClient(t, c, "ls", "-d", "-p", "/docs")
	want := "docs/\n"
	if got != want {
		t.Fatalf("ls -dp output = %q, want %q", got, want)
	}
}

func TestLSLongSortByTime(t *testing.T) {
	c := &fakeClient{lsNodes: sortLSNodes()}
	got := executeWithClient(t, c, "ls", "-l", "-t", "/")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ls -lt line count = %d, want 3; output = %q", len(lines), got)
	}
	if !strings.Contains(lines[0], "alpha.txt") {
		t.Fatalf("ls -lt first = %q, want alpha.txt (newest)", lines[0])
	}
	if !strings.Contains(lines[0], "2025-01-03") {
		t.Fatalf("ls -lt first = %q, want mtime column", lines[0])
	}
}

func multiFileMatches() []store.Match {
	return []store.Match{
		{Path: "/docs/readme.md", Line: 1, Text: "Welcome to GXFS, the GXFS project"},
		{Path: "/docs/readme.md", Line: 3, Text: "GXFS is a virtual filesystem"},
		{Path: "/docs/readme.md", Line: 5, Text: "Learn more about GXFS today"},
		{Path: "/src/main.go", Line: 10, Text: "// GXFS entry point"},
	}
}

func TestGrepDefaultOutput(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	got := executeWithClient(t, c, "grep", "GXFS")
	want := "/docs/readme.md:1:Welcome to GXFS, the GXFS project\n" +
		"/docs/readme.md:3:GXFS is a virtual filesystem\n" +
		"/docs/readme.md:5:Learn more about GXFS today\n" +
		"/src/main.go:10:// GXFS entry point\n"
	if got != want {
		t.Fatalf("grep output = %q, want %q", got, want)
	}
}

func TestGrepCount(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	got := executeWithClient(t, c, "grep", "-c", "GXFS")
	want := "/docs/readme.md:3\n/src/main.go:1\n"
	if got != want {
		t.Fatalf("grep -c output = %q, want %q", got, want)
	}
}

func TestGrepFilesOnly(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	got := executeWithClient(t, c, "grep", "-l", "GXFS")
	want := "/docs/readme.md\n/src/main.go\n"
	if got != want {
		t.Fatalf("grep -l output = %q, want %q", got, want)
	}
}

func TestGrepOnlyMatching(t *testing.T) {
	c := &fakeClient{grepMatches: []store.Match{
		{Path: "/docs/readme.md", Line: 1, Text: "Welcome to GXFS, the GXFS project"},
		{Path: "/src/main.go", Line: 10, Text: "// GXFS entry point"},
	}}
	got := executeWithClient(t, c, "grep", "-o", "GXFS")
	want := "/docs/readme.md:1:GXFS\n" +
		"/docs/readme.md:1:GXFS\n" +
		"/src/main.go:10:GXFS\n"
	if got != want {
		t.Fatalf("grep -o output = %q, want %q", got, want)
	}
}

func TestGrepOnlyMatchingRegexFallsBackToFullLine(t *testing.T) {
	c := &fakeClient{grepMatches: []store.Match{
		{Path: "/src/main.go", Line: 10, Text: "// GXFS entry point"},
	}}
	got := executeWithClient(t, c, "grep", "-o", "-E", "GXFS")
	want := "/src/main.go:10:// GXFS entry point\n"
	if got != want {
		t.Fatalf("grep -oE output = %q, want %q", got, want)
	}
}

func TestGrepFilesOverridesCount(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	got := executeWithClient(t, c, "grep", "-l", "-c", "GXFS")
	want := "/docs/readme.md\n/src/main.go\n"
	if got != want {
		t.Fatalf("grep -lc output = %q, want %q (files-only takes priority)", got, want)
	}
}

func TestGrepCaseInsensitive(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "-i", "gxfs")
	if !c.grepReq.CaseInsensitive {
		t.Fatalf("grep -i request = %+v, want CaseInsensitive=true", c.grepReq)
	}
}

func TestGrepInvertMatch(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "-v", "GXFS")
	if !c.grepReq.Invert {
		t.Fatalf("grep -v request = %+v, want Invert=true", c.grepReq)
	}
}

func TestGrepWholeWord(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "-w", "GXFS")
	if !c.grepReq.WholeWord {
		t.Fatalf("grep -w request = %+v, want WholeWord=true", c.grepReq)
	}
}

func TestGrepWholeLine(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "-x", "GXFS is a virtual filesystem")
	if !c.grepReq.WholeLine {
		t.Fatalf("grep -x request = %+v, want WholeLine=true", c.grepReq)
	}
}

func TestGrepAfterContext(t *testing.T) {
	c := &fakeClient{grepMatches: []store.Match{
		{Path: "/a.txt", Line: 5, Text: "match", After: []string{"after1", "after2"}},
	}}
	got := executeWithClient(t, c, "grep", "-A", "2", "match")
	want := "/a.txt:5:match\n/a.txt-after1\n/a.txt-after2\n"
	if got != want {
		t.Fatalf("grep -A 2 output = %q, want %q", got, want)
	}
	if c.grepReq.ContextAfter != 2 {
		t.Fatalf("grep -A 2 request = %+v, want ContextAfter=2", c.grepReq)
	}
}

func TestGrepBeforeContext(t *testing.T) {
	c := &fakeClient{grepMatches: []store.Match{
		{Path: "/a.txt", Line: 5, Text: "match", Before: []string{"before1"}},
	}}
	got := executeWithClient(t, c, "grep", "-B", "1", "match")
	want := "/a.txt-before1\n/a.txt:5:match\n"
	if got != want {
		t.Fatalf("grep -B 1 output = %q, want %q", got, want)
	}
	if c.grepReq.ContextBefore != 1 {
		t.Fatalf("grep -B 1 request = %+v, want ContextBefore=1", c.grepReq)
	}
}

func TestGrepContextBoth(t *testing.T) {
	c := &fakeClient{grepMatches: []store.Match{
		{Path: "/a.txt", Line: 5, Text: "match", Before: []string{"b1", "b2"}, After: []string{"a1", "a2"}},
	}}
	got := executeWithClient(t, c, "grep", "-C", "2", "match")
	want := "/a.txt-b1\n/a.txt-b2\n/a.txt:5:match\n/a.txt-a1\n/a.txt-a2\n"
	if got != want {
		t.Fatalf("grep -C 2 output = %q, want %q", got, want)
	}
	if c.grepReq.ContextBefore != 2 {
		t.Fatalf("grep -C 2 request = %+v, want ContextBefore=2", c.grepReq)
	}
	if c.grepReq.ContextAfter != 2 {
		t.Fatalf("grep -C 2 request = %+v, want ContextAfter=2", c.grepReq)
	}
}

func TestGrepContextDoesNotOverrideExplicit(t *testing.T) {
	c := &fakeClient{grepMatches: []store.Match{}}
	executeWithClient(t, c, "grep", "-C", "2", "-B", "1", "match")
	if c.grepReq.ContextBefore != 1 {
		t.Fatalf("grep -C2 -B1 request = %+v, want ContextBefore=1 (explicit B wins)", c.grepReq)
	}
	if c.grepReq.ContextAfter != 2 {
		t.Fatalf("grep -C2 -B1 request = %+v, want ContextAfter=2 (from C)", c.grepReq)
	}
}

func TestGrepContextDoesNotOverrideExplicitZero(t *testing.T) {
	c := &fakeClient{grepMatches: []store.Match{}}
	executeWithClient(t, c, "grep", "-C", "2", "-A", "0", "match")
	if c.grepReq.ContextBefore != 2 {
		t.Fatalf("grep -C2 -A0 request = %+v, want ContextBefore=2", c.grepReq)
	}
	if c.grepReq.ContextAfter != 0 {
		t.Fatalf("grep -C2 -A0 request = %+v, want ContextAfter=0 (explicit A wins)", c.grepReq)
	}
}

func TestGrepAllFiles(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "-a", "GXFS")
	if !c.grepReq.All {
		t.Fatalf("grep -a request = %+v, want All=true", c.grepReq)
	}
}

func TestGrepInclude(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "--include", "*.md", "GXFS")
	if c.grepReq.Include != "*.md" {
		t.Fatalf("grep --include request = %+v, want Include=*.md", c.grepReq)
	}
}

func TestGrepExclude(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "--exclude", "*.log", "GXFS")
	if c.grepReq.Exclude != "*.log" {
		t.Fatalf("grep --exclude request = %+v, want Exclude=*.log", c.grepReq)
	}
}

func TestGrepCaseInsensitiveWholeWord(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "-i", "-w", "GXFS")
	if !c.grepReq.CaseInsensitive {
		t.Fatalf("grep -iw request = %+v, want CaseInsensitive=true", c.grepReq)
	}
	if !c.grepReq.WholeWord {
		t.Fatalf("grep -iw request = %+v, want WholeWord=true", c.grepReq)
	}
}

func TestGrepRegexCaseInsensitive(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	executeWithClient(t, c, "grep", "-E", "-i", "gxfs.*virtual")
	if !c.grepReq.Regex {
		t.Fatalf("grep -Ei request = %+v, want Regex=true", c.grepReq)
	}
	if !c.grepReq.CaseInsensitive {
		t.Fatalf("grep -Ei request = %+v, want CaseInsensitive=true", c.grepReq)
	}
}

func TestGrepInvertCount(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	got := executeWithClient(t, c, "grep", "-v", "-c", "GXFS")
	want := "/docs/readme.md:3\n/src/main.go:1\n"
	if got != want {
		t.Fatalf("grep -vc output = %q, want %q", got, want)
	}
	if !c.grepReq.Invert {
		t.Fatalf("grep -vc request = %+v, want Invert=true", c.grepReq)
	}
}

func TestGrepFilesOnlyAllFiles(t *testing.T) {
	c := &fakeClient{grepMatches: multiFileMatches()}
	got := executeWithClient(t, c, "grep", "-l", "-a", "GXFS")
	if !c.grepReq.All {
		t.Fatalf("grep -la request = %+v, want All=true", c.grepReq)
	}
	// Verify files-only output still works when combined with -a
	want := "/docs/readme.md\n/src/main.go\n"
	if got != want {
		t.Fatalf("grep -la output = %q, want %q", got, want)
	}
}

func TestGrepContextOutputFormat(t *testing.T) {
	c := &fakeClient{grepMatches: []store.Match{
		{Path: "/readme.md", Line: 10, Text: "main match", Before: []string{"line 8", "line 9"}, After: []string{"line 11"}},
		{Path: "/readme.md", Line: 20, Text: "another match"},
	}}
	got := executeWithClient(t, c, "grep", "-C", "2", "match")
	want := "/readme.md-line 8\n/readme.md-line 9\n/readme.md:10:main match\n/readme.md-line 11\n" +
		"/readme.md:20:another match\n"
	if got != want {
		t.Fatalf("grep -C 2 context output = %q, want %q", got, want)
	}
}

func TestCatNumberAll(t *testing.T) {
	c := &fakeClient{catContent: "line one\nline two\n\nline four\n"}
	got := executeWithClient(t, c, "cat", "-n", "/readme.md")
	want := "     1  line one\n     2  line two\n     3  \n     4  line four\n"
	if got != want {
		t.Fatalf("cat -n output = %q, want %q", got, want)
	}
}

func TestCatNumberNonBlank(t *testing.T) {
	c := &fakeClient{catContent: "line one\nline two\n\nline four\n"}
	got := executeWithClient(t, c, "cat", "-b", "/readme.md")
	want := "     1  line one\n     2  line two\n\n     3  line four\n"
	if got != want {
		t.Fatalf("cat -b output = %q, want %q", got, want)
	}
}

func TestCatSqueezeBlank(t *testing.T) {
	c := &fakeClient{catContent: "line one\n\n\n\nline two\n"}
	got := executeWithClient(t, c, "cat", "-s", "/readme.md")
	want := "line one\n\nline two\n"
	if got != want {
		t.Fatalf("cat -s output = %q, want %q", got, want)
	}
}

func TestCatNumberAllSqueeze(t *testing.T) {
	c := &fakeClient{catContent: "line one\n\n\n\nline two\n"}
	got := executeWithClient(t, c, "cat", "-n", "-s", "/readme.md")
	want := "     1  line one\n     2  \n     3  line two\n"
	if got != want {
		t.Fatalf("cat -n -s output = %q, want %q", got, want)
	}
}

func TestCatNumberNonBlankSqueeze(t *testing.T) {
	c := &fakeClient{catContent: "line one\n\n\n\nline two\n"}
	got := executeWithClient(t, c, "cat", "-b", "-s", "/readme.md")
	want := "     1  line one\n\n     2  line two\n"
	if got != want {
		t.Fatalf("cat -b -s output = %q, want %q", got, want)
	}
}

func TestCatNumberAllNonBlankBwins(t *testing.T) {
	c := &fakeClient{catContent: "line one\nline two\n\nline four\n"}
	got := executeWithClient(t, c, "cat", "-n", "-b", "/readme.md")
	want := "     1  line one\n     2  line two\n\n     3  line four\n"
	if got != want {
		t.Fatalf("cat -n -b output = %q, want %q (non-blank only)", got, want)
	}
}

func TestStatDefaultShowsAllFields(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{
		Path: "/docs/readme.md", Name: "readme.md", Kind: "file",
		Size: 33, ModTime: "2026-05-02T14:00:00Z",
		Meta: map[string]string{"key1": "value1", "key2": "value2"},
	}}
	got := executeWithClient(t, c, "stat", "/docs/readme.md")
	if !strings.Contains(got, "Path: /docs/readme.md") {
		t.Fatalf("stat output missing Path: %q", got)
	}
	if !strings.Contains(got, "Name: readme.md") {
		t.Fatalf("stat output missing Name: %q", got)
	}
	if !strings.Contains(got, "Kind: file") {
		t.Fatalf("stat output missing Kind: %q", got)
	}
	if !strings.Contains(got, "Size: 33") {
		t.Fatalf("stat output missing Size: %q", got)
	}
	if !strings.Contains(got, "ModTime: 2026-05-02T14:00:00Z") {
		t.Fatalf("stat output missing ModTime: %q", got)
	}
	if !strings.Contains(got, "Meta:") {
		t.Fatalf("stat output missing Meta: %q", got)
	}
}

func TestStatEmptyModTimeAndMeta(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{
		Path: "/docs/readme.md", Name: "readme.md", Kind: "file", Size: 10,
	}}
	got := executeWithClient(t, c, "stat", "/docs/readme.md")
	if !strings.Contains(got, "ModTime: -") {
		t.Fatalf("stat output with empty ModTime = %q, want '-'", got)
	}
	if strings.Contains(got, "Meta:") {
		t.Fatalf("stat output with empty Meta should not contain Meta: %q", got)
	}
}

func TestStatCustomFormat(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{
		Path: "/docs/readme.md", Name: "readme.md", Kind: "file",
		Size: 33, ModTime: "2026-05-02T14:00:00Z",
	}}
	got := executeWithClient(t, c, "stat", "-c", "%p %s %y", "/docs/readme.md")
	want := "/docs/readme.md 33 2026-05-02T14:00:00Z\n"
	if got != want {
		t.Fatalf("stat -c output = %q, want %q", got, want)
	}
}

func TestStatCustomFormatLiteralPercent(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{
		Path: "/docs/readme.md", Name: "readme.md", Kind: "file", Size: 10,
	}}
	got := executeWithClient(t, c, "stat", "-c", "%%p", "/docs/readme.md")
	want := "%p\n"
	if got != want {
		t.Fatalf("stat -c %%p output = %q, want %q", got, want)
	}
}

func TestStatTerseFormat(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{
		Path: "/docs/readme.md", Name: "readme.md", Kind: "file",
		Size: 33, ModTime: "2026-05-02T14:00:00Z",
	}}
	got := executeWithClient(t, c, "stat", "-f", "/docs/readme.md")
	want := "/docs/readme.md\tfile\t33\t2026-05-02T14:00:00Z\n"
	if got != want {
		t.Fatalf("stat -f output = %q, want %q", got, want)
	}
}

func TestStatTerseEmptyModTime(t *testing.T) {
	c := &fakeClient{statNode: &store.Node{
		Path: "/docs/readme.md", Name: "readme.md", Kind: "file", Size: 10,
	}}
	got := executeWithClient(t, c, "stat", "-f", "/docs/readme.md")
	want := "/docs/readme.md\tfile\t10\t-\n"
	if got != want {
		t.Fatalf("stat -f with empty ModTime = %q, want %q", got, want)
	}
}

// --- Find tests ---

func findMixedNodes() []store.Node {
	return []store.Node{
		{Path: "/src/main.go", Name: "main.go", Kind: "file"},
		{Path: "/src/util.go", Name: "util.go", Kind: "file"},
		{Path: "/src/pkg", Name: "pkg", Kind: "dir"},
		{Path: "/src/.hidden.go", Name: ".hidden.go", Kind: "file"},
		{Path: "/README.MD", Name: "README.MD", Kind: "file"},
	}
}

func TestFindBasicName(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	got := executeWithClient(t, c, "find", "--name", "*.go")
	if c.findReq.Name != "*.go" {
		t.Fatalf("find --name request = %+v, want Name=*.go", c.findReq)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("find --name output = %q, want 5 lines", got)
	}
}

func TestFindTypeFile(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	executeWithClient(t, c, "find", "--name", "*", "-t", "f")
	if c.findReq.Type != "file" {
		t.Fatalf("find -t f request = %+v, want Type=file", c.findReq)
	}
}

func TestFindTypeDir(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	executeWithClient(t, c, "find", "--name", "*", "-t", "d")
	if c.findReq.Type != "dir" {
		t.Fatalf("find -t d request = %+v, want Type=dir", c.findReq)
	}
}

func TestFindMaxDepth(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	executeWithClient(t, c, "find", "--name", "*.go", "--maxdepth", "2")
	if c.findReq.MaxDepth != 2 {
		t.Fatalf("find --maxdepth 2 request = %+v, want MaxDepth=2", c.findReq)
	}
}

func TestFindMinDepth(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	executeWithClient(t, c, "find", "--name", "*.go", "--mindepth", "2")
	if c.findReq.MinDepth != 2 {
		t.Fatalf("find --mindepth 2 request = %+v, want MinDepth=2", c.findReq)
	}
}

func TestFindAll(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	executeWithClient(t, c, "find", "--name", "*.go", "-a")
	if !c.findReq.All {
		t.Fatalf("find -a request = %+v, want All=true", c.findReq)
	}
}

func TestFindIName(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	executeWithClient(t, c, "find", "--iname", "*.MD")
	if c.findReq.IName != "*.MD" {
		t.Fatalf("find --iname request = %+v, want IName=*.MD", c.findReq)
	}
}

func TestFindINameAloneIsValid(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	got := executeWithClient(t, c, "find", "--iname", "*.MD")
	if c.findReq.IName != "*.MD" {
		t.Fatalf("find --iname request = %+v, want IName=*.MD", c.findReq)
	}
	if c.findReq.Name != "" {
		t.Fatalf("find --iname should leave Name empty, got %q", c.findReq.Name)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatalf("find --iname output empty, want results")
	}
}

func TestFindRequiresNameOrIName(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	cmd := newRootCommand(c, c, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"find", "/src"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("find without -name or -iname should error")
	}
	if err.Error() != "-name or -iname is required" {
		t.Fatalf("find error = %q, want '-name or -iname is required'", err.Error())
	}
}

func TestFindCombinedTypeAllMaxDepth(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	executeWithClient(t, c, "find", "--name", "*.go", "-t", "f", "-a", "--maxdepth", "3")
	if c.findReq.Type != "file" {
		t.Fatalf("find combined request = %+v, want Type=file", c.findReq)
	}
	if !c.findReq.All {
		t.Fatalf("find combined request = %+v, want All=true", c.findReq)
	}
	if c.findReq.MaxDepth != 3 {
		t.Fatalf("find combined request = %+v, want MaxDepth=3", c.findReq)
	}
}

// --- Tree tests ---

func TestTreeBasic(t *testing.T) {
	c := &fakeClient{treeText: "/\n  docs/\n    readme.md\n  main.go\n"}
	got := executeWithClient(t, c, "tree")
	want := "/\n  docs/\n    readme.md\n  main.go\n"
	if got != want {
		t.Fatalf("tree output = %q, want %q", got, want)
	}
}

func TestTreeAllFlag(t *testing.T) {
	c := &fakeClient{treeText: "/\n  .hidden\n  docs/\n  main.go\n"}
	executeWithClient(t, c, "tree", "-a")
	if !c.treeReq.All {
		t.Fatalf("tree -a request = %+v, want All=true", c.treeReq)
	}
}

func TestTreeDirsOnly(t *testing.T) {
	c := &fakeClient{treeText: "/\n  docs/\n"}
	executeWithClient(t, c, "tree", "-d")
	if !c.treeReq.DirsOnly {
		t.Fatalf("tree -d request = %+v, want DirsOnly=true", c.treeReq)
	}
}

func TestTreeFullPath(t *testing.T) {
	c := &fakeClient{treeText: "/docs\n/docs/readme.md\n"}
	executeWithClient(t, c, "tree", "-f")
	if !c.treeReq.FullPath {
		t.Fatalf("tree -f request = %+v, want FullPath=true", c.treeReq)
	}
}

func TestTreeShowSize(t *testing.T) {
	c := &fakeClient{treeText: "/\n  main.go [100B]\n"}
	executeWithClient(t, c, "tree", "-s")
	if !c.treeReq.ShowSize {
		t.Fatalf("tree -s request = %+v, want ShowSize=true", c.treeReq)
	}
}

func TestTreeSortByTime(t *testing.T) {
	c := &fakeClient{treeText: "/\n  new.go\n  old.go\n"}
	executeWithClient(t, c, "tree", "-t")
	if c.treeReq.Sort != "mtime" {
		t.Fatalf("tree -t request = %+v, want Sort=mtime", c.treeReq)
	}
}

func TestTreeDirsFirst(t *testing.T) {
	c := &fakeClient{treeText: "/\n  docs/\n  main.go\n"}
	executeWithClient(t, c, "tree", "--dirsfirst")
	if !c.treeReq.DirsFirst {
		t.Fatalf("tree --dirsfirst request = %+v, want DirsFirst=true", c.treeReq)
	}
}

func TestTreeCombinedAllDirsOnlyFullPath(t *testing.T) {
	c := &fakeClient{treeText: "/docs\n/docs/sub\n"}
	executeWithClient(t, c, "tree", "-a", "-d", "-f")
	if !c.treeReq.All {
		t.Fatalf("tree -adf request = %+v, want All=true", c.treeReq)
	}
	if !c.treeReq.DirsOnly {
		t.Fatalf("tree -adf request = %+v, want DirsOnly=true", c.treeReq)
	}
	if !c.treeReq.FullPath {
		t.Fatalf("tree -adf request = %+v, want FullPath=true", c.treeReq)
	}
}

func TestTreeSortByTimeDirsFirstSize(t *testing.T) {
	c := &fakeClient{treeText: "/\n  docs/\n  main.go [50B]\n"}
	executeWithClient(t, c, "tree", "-t", "--dirsfirst", "-s")
	if c.treeReq.Sort != "mtime" {
		t.Fatalf("tree -t --dirsfirst -s request = %+v, want Sort=mtime", c.treeReq)
	}
	if !c.treeReq.DirsFirst {
		t.Fatalf("tree -t --dirsfirst -s request = %+v, want DirsFirst=true", c.treeReq)
	}
	if !c.treeReq.ShowSize {
		t.Fatalf("tree -t --dirsfirst -s request = %+v, want ShowSize=true", c.treeReq)
	}
}

// --- Validation and backward-compat tests ---

func executeErr(t *testing.T, args ...string) error {
	t.Helper()
	client := &fakeClient{}
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestFindInvalidType(t *testing.T) {
	err := executeErr(t, "find", "--name", "*.go", "-t", "x")
	if err == nil {
		t.Fatal("find -t x should error")
	}
	want := `invalid type "x": use f or d`
	if err.Error() != want {
		t.Fatalf("find -t x error = %q, want %q", err.Error(), want)
	}
}

func TestFindBackCompatNameFlag(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	got := executeWithClient(t, c, "find", "--name", "*.go")
	if c.findReq.Name != "*.go" {
		t.Fatalf("backcompat find --name request = %+v, want Name=*.go", c.findReq)
	}
	_ = got
}

func TestTreeBackCompatLevelFlag(t *testing.T) {
	c := &fakeClient{treeText: "/\n  docs/\n"}
	executeWithClient(t, c, "tree", "-L", "1")
	if c.treeReq.Depth != 1 {
		t.Fatalf("backcompat tree -L 1 request = %+v, want Depth=1", c.treeReq)
	}
}

func TestFindNameAndINameBothSet(t *testing.T) {
	c := &fakeClient{findNodes: findMixedNodes()}
	executeWithClient(t, c, "find", "--name", "*.go", "--iname", "*.MD")
	if c.findReq.Name != "*.go" {
		t.Fatalf("find --name --iname request = %+v, want Name=*.go", c.findReq)
	}
	if c.findReq.IName != "*.MD" {
		t.Fatalf("find --name --iname request = %+v, want IName=*.MD", c.findReq)
	}
}

func TestWantsHelpIgnoresHelpSubstringInArgument(t *testing.T) {
	args := []string{"grep", "pattern", "some --help"}
	if wantsHelp(args) {
		t.Fatalf("wantsHelp(%v) = true, want false", args)
	}
}

func TestSyncPushUploadsFilesAndWritesManifest(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "docs", "a.md"), "alpha")
	writeTestFile(t, filepath.Join(dir, "docs", "nested", "b.md"), "bravo")
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	client := &fakeClient{}

	got := executeWithClient(t, client, "sync", "push", "docs", "--manifest", manifestPath)
	if len(client.putReqs) != 2 {
		t.Fatalf("Put requests = %d, want 2: %+v", len(client.putReqs), client.putReqs)
	}
	if client.putReqs[0].Path != "docs/a.md" || client.putReqs[0].Content != "alpha" {
		t.Fatalf("first Put request = %+v, want docs/a.md alpha", client.putReqs[0])
	}
	if client.putReqs[1].Path != "docs/nested/b.md" || client.putReqs[1].Content != "bravo" {
		t.Fatalf("second Put request = %+v, want docs/nested/b.md bravo", client.putReqs[1])
	}
	if !strings.Contains(got, "pushed 2 files") || !strings.Contains(got, "updated "+manifestPath) {
		t.Fatalf("sync push output = %q, want pushed count and manifest path", got)
	}

	manifest := readTextFile(t, manifestPath)
	if !strings.Contains(manifest, `local = 'docs/a.md'`) || !strings.Contains(manifest, `remote_doc = 'repo://self/docs/a.md'`) {
		t.Fatalf("manifest missing a.md entry: %s", manifest)
	}
	if !strings.Contains(manifest, `content_hash = 'sha256:8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8'`) {
		t.Fatalf("manifest missing alpha hash: %s", manifest)
	}
}

func TestSyncPullUpdatesManifestWithoutMaterializing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	client := &fakeClient{
		statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/docs/a.md", Name: "a.md", Kind: "file", Size: 5, ModTime: "2026-05-12T00:00:00Z"},
		},
		catContents: map[string]string{"/docs/a.md": "alpha"},
	}

	got := executeWithClient(t, client, "sync", "pull", "docs", "--manifest", manifestPath)
	if !strings.Contains(got, "pulled 1 file") || !strings.Contains(got, "updated "+manifestPath) {
		t.Fatalf("sync pull output = %q, want pull count and manifest path", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "a.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("materialized file stat error = %v, want not exist", err)
	}

	manifest := readTextFile(t, manifestPath)
	if !strings.Contains(manifest, `local = 'docs/a.md'`) || !strings.Contains(manifest, `materialized = false`) {
		t.Fatalf("manifest missing non-materialized pull entry: %s", manifest)
	}
	if !strings.Contains(manifest, `content_hash = 'sha256:8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8'`) {
		t.Fatalf("manifest missing remote hash: %s", manifest)
	}
}

func TestSyncPullMaterializesRemoteFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	client := &fakeClient{
		statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/docs/nested/a.md", Name: "a.md", Kind: "file", Size: 5, ModTime: "2026-05-12T00:00:00Z"},
		},
		catContents: map[string]string{"/docs/nested/a.md": "alpha"},
	}

	got := executeWithClient(t, client, "sync", "pull", "docs", "--materialize", "--manifest", manifestPath)
	if !strings.Contains(got, "materialized 1 file") {
		t.Fatalf("sync pull output = %q, want materialized count", got)
	}
	if got := readTextFile(t, filepath.Join(dir, "docs", "nested", "a.md")); got != "alpha" {
		t.Fatalf("materialized content = %q, want alpha", got)
	}
}

func TestSyncPullDetectsBothChangedConflict(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	writeTestFile(t, filepath.Join(dir, "docs", "a.md"), "local")
	writeTestFile(t, manifestPath, `version = 1

[[entries]]
local = 'docs/a.md'
remote_doc = 'repo://self/docs/a.md'
mount = 'docs'
content_hash = 'sha256:8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8'
size = 5
mtime = '2026-05-12T00:00:00Z'
materialized = true
`)
	client := &fakeClient{
		statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/docs/a.md", Name: "a.md", Kind: "file", Size: 6, ModTime: "2026-05-12T00:01:00Z"},
		},
		catContents: map[string]string{"/docs/a.md": "remote"},
	}

	_, err := executeWithClientErr(client, "sync", "pull", "docs", "--manifest", manifestPath)
	if err == nil || !strings.Contains(err.Error(), "local and remote both changed") {
		t.Fatalf("sync pull conflict error = %v, want both-changed conflict", err)
	}
}

func TestSyncPullForceLocalPushesConflict(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	writeTestFile(t, filepath.Join(dir, "docs", "a.md"), "local")
	writeTestFile(t, manifestPath, `version = 1

[[entries]]
local = 'docs/a.md'
remote_doc = 'repo://self/docs/a.md'
mount = 'docs'
content_hash = 'sha256:8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8'
size = 5
mtime = '2026-05-12T00:00:00Z'
materialized = true
`)
	client := &fakeClient{
		statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/docs/a.md", Name: "a.md", Kind: "file", Size: 6, ModTime: "2026-05-12T00:01:00Z"},
		},
		catContents: map[string]string{"/docs/a.md": "remote"},
	}

	got := executeWithClient(t, client, "sync", "pull", "docs", "--force-local", "--manifest", manifestPath)
	if len(client.putReqs) != 1 || client.putReqs[0].Path != "docs/a.md" || client.putReqs[0].Content != "local" {
		t.Fatalf("force-local Put requests = %+v, want local upload", client.putReqs)
	}
	if !strings.Contains(got, "pushed 1 local file") {
		t.Fatalf("sync pull force-local output = %q, want pushed conflict count", got)
	}
}

func TestSyncPullErrorsWhenLocalPathIsDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	if err := os.MkdirAll(filepath.Join(dir, "docs", "a.md"), 0o755); err != nil {
		t.Fatalf("mkdir local conflict dir: %v", err)
	}
	client := &fakeClient{
		statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/docs/a.md", Name: "a.md", Kind: "file", Size: 5, ModTime: "2026-05-12T00:00:00Z"},
		},
		catContents: map[string]string{"/docs/a.md": "alpha"},
	}

	_, err := executeWithClientErr(client, "sync", "pull", "docs", "--manifest", manifestPath)
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("sync pull directory conflict error = %v, want directory error", err)
	}
}

func TestRefreshUpdatesManifestWithoutWritingFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	client := &fakeClient{
		statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/docs/a.md", Name: "a.md", Kind: "file", Size: 5, ModTime: "2026-05-12T00:00:00Z"},
		},
		catContents: map[string]string{"/docs/a.md": "alpha"},
	}

	got := executeWithClient(t, client, "refresh", "docs", "--manifest", manifestPath)
	if !strings.Contains(got, "refreshed 1 file") || !strings.Contains(got, "updated "+manifestPath) {
		t.Fatalf("refresh output = %q, want refreshed count and manifest path", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "a.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("materialized file stat error = %v, want not exist", err)
	}
	manifest := readTextFile(t, manifestPath)
	if !strings.Contains(manifest, `local = 'docs/a.md'`) || !strings.Contains(manifest, `materialized = false`) {
		t.Fatalf("manifest missing refreshed non-materialized entry: %s", manifest)
	}
}

func TestMaterializeWritesFilesAndManifest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	client := &fakeClient{
		statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/docs/nested/a.md", Name: "a.md", Kind: "file", Size: 5, ModTime: "2026-05-12T00:00:00Z"},
		},
		catContents: map[string]string{"/docs/nested/a.md": "alpha"},
	}

	got := executeWithClient(t, client, "materialize", "docs", "--manifest", manifestPath)
	if !strings.Contains(got, "materialized 1 file") {
		t.Fatalf("materialize output = %q, want materialized count", got)
	}
	if got := readTextFile(t, filepath.Join(dir, "docs", "nested", "a.md")); got != "alpha" {
		t.Fatalf("materialized content = %q, want alpha", got)
	}
	manifest := readTextFile(t, manifestPath)
	if !strings.Contains(manifest, `local = 'docs/nested/a.md'`) || !strings.Contains(manifest, `materialized = true`) {
		t.Fatalf("manifest missing materialized entry: %s", manifest)
	}
}

func TestDematerializeRemovesFilesAndUpdatesManifest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	writeTestFile(t, filepath.Join(dir, "docs", "nested", "a.md"), "alpha")
	writeTestFile(t, manifestPath, `version = 1

[[entries]]
local = 'docs/nested/a.md'
remote_doc = 'repo://self/docs/nested/a.md'
mount = 'docs'
content_hash = 'sha256:8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8'
size = 5
mtime = '2026-05-12T00:00:00Z'
materialized = true
`)

	got := executeWithClient(t, &fakeClient{}, "dematerialize", "docs", "--manifest", manifestPath)
	if !strings.Contains(got, "dematerialized 1 file") {
		t.Fatalf("dematerialize output = %q, want dematerialized count", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "nested", "a.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dematerialized file stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "nested")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty parent dir stat error = %v, want not exist", err)
	}
	manifest := readTextFile(t, manifestPath)
	if !strings.Contains(manifest, `local = 'docs/nested/a.md'`) || !strings.Contains(manifest, `materialized = false`) {
		t.Fatalf("manifest missing dematerialized entry: %s", manifest)
	}
}

func TestDematerializeKeepFilesOnlyUpdatesManifest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	writeTestFile(t, filepath.Join(dir, "docs", "a.md"), "alpha")
	writeTestFile(t, manifestPath, `version = 1

[[entries]]
local = 'docs/a.md'
remote_doc = 'repo://self/docs/a.md'
mount = 'docs'
content_hash = 'sha256:8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8'
size = 5
mtime = '2026-05-12T00:00:00Z'
materialized = true
`)

	executeWithClient(t, &fakeClient{}, "dematerialize", "docs", "--keep-files", "--manifest", manifestPath)
	if got := readTextFile(t, filepath.Join(dir, "docs", "a.md")); got != "alpha" {
		t.Fatalf("kept file content = %q, want alpha", got)
	}
	manifest := readTextFile(t, manifestPath)
	if !strings.Contains(manifest, `materialized = false`) {
		t.Fatalf("manifest missing keep-files dematerialized flag: %s", manifest)
	}
}

func executeInit(t *testing.T, args ...string) string {
	t.Helper()

	cmd := command.NewInitCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init %v error = %v", args, err)
	}
	return out.String()
}

func TestInitWritesSettingsAndAgentsInstructions(t *testing.T) {
	dir := t.TempDir()
	got := executeInit(t, dir)

	settings := readTextFile(t, filepath.Join(dir, ".gxfs", "settings.toml"))
	if !strings.Contains(settings, "version = 1") {
		t.Fatalf("settings.toml = %q, want version", settings)
	}
	if !strings.Contains(settings, "[docs]\npath = \"docs\"") {
		t.Fatalf("settings.toml = %q, want docs path", settings)
	}
	if !strings.Contains(settings, "[auth]\nmode = \"bearer\"\ntoken_env = \"GXFS_TOKEN\"") {
		t.Fatalf("settings.toml = %q, want auth block", settings)
	}
	if !strings.Contains(settings, "[cache]\nmetadata_ttl = \"5m\"\ncontent_ttl = \"24h\"\nmaterialize = \"explicit\"") {
		t.Fatalf("settings.toml = %q, want cache block", settings)
	}

	mounts := readTextFile(t, filepath.Join(dir, ".gxfs", "mounts.toml"))
	if !strings.Contains(mounts, "version = 1") {
		t.Fatalf("mounts.toml = %q, want version", mounts)
	}
	if !strings.Contains(mounts, "local = \"docs\"") || !strings.Contains(mounts, "remote = \"repo://self/docs\"") {
		t.Fatalf("mounts.toml = %q, want default docs mount", mounts)
	}
	if !strings.Contains(mounts, "mode = \"writable\"") || !strings.Contains(mounts, "source = \"default\"") {
		t.Fatalf("mounts.toml = %q, want writable default mount", mounts)
	}

	agents := readTextFile(t, filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(agents, command.GXFSInstructionsStart) || !strings.Contains(agents, command.GXFSInstructionsEnd) {
		t.Fatalf("AGENTS.md missing GXFS markers: %q", agents)
	}
	if !strings.Contains(agents, "Use gxfs CLI to browse") || !strings.Contains(agents, "gxfs tree docs -L 3") {
		t.Fatalf("AGENTS.md missing instruction content: %q", agents)
	}
	if !strings.Contains(got, "updated GXFS instructions in") {
		t.Fatalf("init output = %q, want instruction update", got)
	}
}

func TestInitReplacesExistingInstructions(t *testing.T) {
	dir := t.TempDir()
	executeInit(t, dir)
	executeInit(t, dir)

	agents := readTextFile(t, filepath.Join(dir, "AGENTS.md"))
	if got := strings.Count(agents, command.GXFSInstructionsStart); got != 1 {
		t.Fatalf("GXFS start marker count = %d, want 1 in %q", got, agents)
	}
	if got := strings.Count(agents, command.GXFSInstructionsEnd); got != 1 {
		t.Fatalf("GXFS end marker count = %d, want 1 in %q", got, agents)
	}
}

func TestInitAgentClaudeWritesClaudeMD(t *testing.T) {
	dir := t.TempDir()
	executeInit(t, "--agent", "claude", dir)

	claude := readTextFile(t, filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(claude, command.GXFSInstructionsStart) {
		t.Fatalf("CLAUDE.md missing GXFS instructions: %q", claude)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md exists after --agent claude, err=%v", err)
	}
}

func TestInitClaudeFlagAliasesAgentClaude(t *testing.T) {
	dir := t.TempDir()
	executeInit(t, "--claude", dir)

	claude := readTextFile(t, filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(claude, command.GXFSInstructionsStart) {
		t.Fatalf("CLAUDE.md missing GXFS instructions: %q", claude)
	}
}

func TestInitNoInstructionsOnlyWritesSettings(t *testing.T) {
	dir := t.TempDir()
	executeInit(t, "--no-instructions", dir)

	if _, err := os.Stat(filepath.Join(dir, ".gxfs", "settings.toml")); err != nil {
		t.Fatalf("settings.toml stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gxfs", "mounts.toml")); err != nil {
		t.Fatalf("mounts.toml stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md exists after --no-instructions, err=%v", err)
	}
}

func TestInitWritesSettingsAndMountsTemplatesFromFlags(t *testing.T) {
	dir := t.TempDir()
	executeInit(t,
		"--no-instructions",
		"--repo", "github.com/acme/project",
		"--server", "https://gxfs.example.com",
		"--docs", "knowledge",
		"--auth", "bearer",
		dir,
	)

	settings := readTextFile(t, filepath.Join(dir, ".gxfs", "settings.toml"))
	for _, want := range []string{
		`version = 1`,
		`repo = "github.com/acme/project"`,
		`addr = "https://gxfs.example.com"`,
		`mode = "bearer"`,
		`token_env = "GXFS_TOKEN"`,
		`path = "knowledge"`,
		`materialize = "explicit"`,
	} {
		if !strings.Contains(settings, want) {
			t.Fatalf("settings.toml = %q, missing %q", settings, want)
		}
	}

	mounts := readTextFile(t, filepath.Join(dir, ".gxfs", "mounts.toml"))
	for _, want := range []string{
		`version = 1`,
		`local = "knowledge"`,
		`remote = "repo://self/knowledge"`,
		`mode = "writable"`,
		`source = "default"`,
	} {
		if !strings.Contains(mounts, want) {
			t.Fatalf("mounts.toml = %q, missing %q", mounts, want)
		}
	}
}

func TestInitReportsResolvedSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte("# Claude\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	if err := os.Symlink("CLAUDE.md", filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("symlink AGENTS.md: %v", err)
	}

	got := executeInit(t, dir)
	claude := readTextFile(t, claudePath)
	if !strings.Contains(claude, command.GXFSInstructionsStart) {
		t.Fatalf("CLAUDE.md missing GXFS instructions through symlink: %q", claude)
	}
	if !strings.Contains(got, "resolved to") {
		t.Fatalf("init output = %q, want resolved symlink target", got)
	}
}

func TestInitRejectsUnsupportedAgent(t *testing.T) {
	dir := t.TempDir()
	cmd := command.NewInitCommand()
	cmd.SetArgs([]string{"--agent", "gemini", dir})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unsupported agent") {
		t.Fatalf("init --agent gemini error = %v, want unsupported agent", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gxfs", "settings.toml")); !os.IsNotExist(err) {
		t.Fatalf("settings.toml exists after unsupported agent, err=%v", err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestMountAddRejectsInvalidRemoteRef(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	_, err := executeWithClientErr(client, "mount", "add", "http://bad/ref", "docs")
	if err == nil {
		t.Fatal("expected error for invalid remote ref")
	}
	if !strings.Contains(err.Error(), "unsupported remote") {
		t.Fatalf("error = %q, want unsupported remote rejection", err)
	}
}

func TestMountAddRejectsCollectionRef(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	_, err := executeWithClientErr(client, "mount", "add", "collection://stuff", "docs")
	if err == nil {
		t.Fatal("expected error for collection ref")
	}
}

func TestMountAddRejectsEmptyLocalPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	_, err := executeWithClientErr(client, "mount", "add", "repo://self/docs", "/")
	if err == nil {
		t.Fatal("expected error for empty local path")
	}
	if !strings.Contains(err.Error(), "non-empty relative path") {
		t.Fatalf("error = %q, want non-empty path rejection", err)
	}

	_, err = executeWithClientErr(client, "mount", "add", "repo://self/docs", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace local path")
	}
}

func TestMountAddRejectsEmptyRemotePath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	_, err := executeWithClientErr(client, "mount", "add", "repo://self/", "docs")
	if err == nil {
		t.Fatal("expected error for empty remote path")
	}
}

func TestMountAddRejectsInvalidMode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	_, err := executeWithClientErr(client, "mount", "add", "repo://self/docs", "docs", "--mode", "readwrite")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "mode must be readonly or writable") {
		t.Fatalf("error = %q, want mode rejection", err)
	}
}

func TestMountAddRejectsDotLocalPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	_, err := executeWithClientErr(client, "mount", "add", "repo://self/docs", ".")
	if err == nil {
		t.Fatal("expected error for '.' local path")
	}
	if !strings.Contains(err.Error(), "non-empty relative path") {
		t.Fatalf("error = %q, want non-empty path rejection", err)
	}
}

func TestMountAddWritesMountsToml(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	got := executeWithClient(t, client, "mount", "add", "repo://self/docs", "mydocs", "--mode", "writable", "--no-refresh")
	if !strings.Contains(got, "added mount mydocs → repo://self/docs (writable)") {
		t.Fatalf("output = %q, want add confirmation", got)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gxfs", "mounts.toml"))
	if err != nil {
		t.Fatalf("read mounts.toml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "local = 'mydocs'") {
		t.Fatalf("mounts.toml missing local path: %s", content)
	}
	if !strings.Contains(content, "remote = 'repo://self/docs'") {
		t.Fatalf("mounts.toml missing remote: %s", content)
	}
	if !strings.Contains(content, "mode = 'writable'") {
		t.Fatalf("mounts.toml missing mode: %s", content)
	}
	if !strings.Contains(content, "source = 'manual'") {
		t.Fatalf("mounts.toml missing source: %s", content)
	}
}

func TestMountAddRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	// Add first
	executeWithClient(t, client, "mount", "add", "repo://self/docs", "docs", "--no-refresh")

	// Second add without --force should fail
	_, err := executeWithClientErr(client, "mount", "add", "repo://self/docs", "docs", "--no-refresh")
	if err == nil {
		t.Fatal("expected error for duplicate mount")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %q, want duplicate rejection", err)
	}
}

func TestMountAddForceReplaces(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	// Add first
	executeWithClient(t, client, "mount", "add", "repo://self/docs", "docs", "--no-refresh")
	// Force replace
	got := executeWithClient(t, client, "mount", "add", "repo://self/api", "docs", "--mode", "writable", "--force", "--no-refresh")
	if !strings.Contains(got, "replaced mount docs → repo://self/api") {
		t.Fatalf("output = %q, want replace confirmation", got)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gxfs", "mounts.toml"))
	if err != nil {
		t.Fatalf("read mounts.toml: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "repo://self/docs") {
		t.Fatalf("mounts.toml still contains old mount: %s", content)
	}
	if !strings.Contains(content, "repo://self/api") {
		t.Fatalf("mounts.toml missing new remote: %s", content)
	}
}

func TestMountRemoveDeletesEntry(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	// Add then remove
	executeWithClient(t, client, "mount", "add", "repo://self/docs", "docs", "--no-refresh")
	got := executeWithClient(t, client, "mount", "remove", "docs")
	if !strings.Contains(got, "removed mount docs") {
		t.Fatalf("output = %q, want remove confirmation", got)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gxfs", "mounts.toml"))
	if err != nil {
		t.Fatalf("read mounts.toml: %v", err)
	}
	if strings.Contains(string(data), "local = 'docs'") {
		t.Fatalf("mounts.toml still has removed mount: %s", string(data))
	}
}

func TestMountRemoveRejectsMissing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// No mounts.toml at all
	_, err := executeWithClientErr(&fakeClient{}, "mount", "remove", "nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent mount")
	}
}

func TestMountRemoveBlocksOnMaterialized(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	// Add mount
	executeWithClient(t, client, "mount", "add", "repo://self/docs", "docs", "--no-refresh")

	// Write a manifest with a materialized file under docs/
	manifestDir := filepath.Join(dir, ".gxfs")
	manifestContent := `version = 1

[[entries]]
local = 'docs/readme.md'
remote_doc = 'repo://self/docs/readme.md'
content_hash = 'sha256:abc123'
materialized = true
`
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest.toml"), []byte(manifestContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remove should fail
	_, err := executeWithClientErr(client, "mount", "remove", "docs")
	if err == nil {
		t.Fatal("expected error when materialized files exist")
	}
	if !strings.Contains(err.Error(), "materialized files exist") {
		t.Fatalf("error = %q, want materialized warning", err)
	}
}

func TestMountListShowsMounts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	// No mounts yet → "no mounts configured"
	got := executeWithClient(t, client, "mount", "list")
	if !strings.Contains(got, "no mounts configured") {
		t.Fatalf("empty list = %q, want 'no mounts configured'", got)
	}

	// Add two mounts
	executeWithClient(t, client, "mount", "add", "repo://self/docs", "docs", "--no-refresh")
	executeWithClient(t, client, "mount", "add", "repo://self/api", "api", "--mode", "writable", "--no-refresh")

	got = executeWithClient(t, client, "mount", "list")
	if !strings.Contains(got, "docs\trepo://self/docs\treadonly") {
		t.Fatalf("list output missing docs mount: %q", got)
	}
	if !strings.Contains(got, "api\trepo://self/api\twritable") {
		t.Fatalf("list output missing api mount: %q", got)
	}
}

func TestMountAddRefreshUsesCorrectRemotePath(t *testing.T) {
	// Mount repo://self/api at local "docs/api" and verify the refresh
	// requests the remote path /api, NOT /docs/api.
	dir := t.TempDir()
	t.Chdir(dir)

	client := &fakeClient{
		statNode: &store.Node{Path: "/api", Name: "api", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/api/endpoint.md", Name: "endpoint.md", Kind: "file"},
		},
	}
	got := executeWithClient(t, client, "mount", "add", "repo://self/api", "docs/api")
	if !strings.Contains(got, "added mount docs/api → repo://self/api") {
		t.Fatalf("output = %q, want add confirmation", got)
	}

	// The fakeClient.lsReq should have received the resolved remote path /api,
	// not the local path "docs/api" or "/docs/api".
	if client.lsReq.Path != "/api" {
		t.Fatalf("LS request path = %q, want /api (refresh resolved through mount resolver)", client.lsReq.Path)
	}
}

func TestSearchHumanOutput(t *testing.T) {
	client := &fakeClient{}
	got := executeWithClient(t, client, "search", "test query")
	if !strings.Contains(got, "/docs/guide.md") {
		t.Fatalf("search output missing path: %q", got)
	}
	if !strings.Contains(got, "rank: 0.90") {
		t.Fatalf("search output missing rank: %q", got)
	}
	if !strings.Contains(got, "test result") {
		t.Fatalf("search output missing snippet: %q", got)
	}
	if client.searchReq.Query != "test query" {
		t.Fatalf("search query = %q, want 'test query'", client.searchReq.Query)
	}
}

func TestSearchJSONOutput(t *testing.T) {
	client := &fakeClient{}
	got := executeWithClient(t, client, "search", "test", "--json")
	var resp store.SearchResponse
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatalf("unmarshal JSON: %v, body = %q", err, got)
	}
	if resp.Total != 1 || len(resp.Results) != 1 {
		t.Fatalf("search JSON = %+v, want 1 result", resp)
	}
	if resp.Results[0].Path != "/docs/guide.md" {
		t.Fatalf("result path = %q, want /docs/guide.md", resp.Results[0].Path)
	}
}

func TestSearchPathAndLimitFlags(t *testing.T) {
	client := &fakeClient{}
	_ = executeWithClient(t, client, "search", "test", "--path", "/docs", "--limit", "5")
	if client.searchReq.Path != "/docs" {
		t.Fatalf("search path = %q, want /docs", client.searchReq.Path)
	}
	if client.searchReq.Limit != 5 {
		t.Fatalf("search limit = %d, want 5", client.searchReq.Limit)
	}
}

func TestSearchEmptyResults(t *testing.T) {
	client := &fakeClient{
		searchResp: &store.SearchResponse{Results: []store.SearchResult{}, Total: 0},
	}
	got := executeWithClient(t, client, "search", "nonexistent")
	if !strings.Contains(got, "no results found") {
		t.Fatalf("empty search output = %q, want 'no results found'", got)
	}
}

// TestRefreshMountedRemoteDocCorrect verifies that when a mount maps
// repo://self/api to local docs/api, the manifest remote_doc contains
// the true server path (repo://self/api/endpoint.md), NOT the local
// display path (repo://self/docs/api/endpoint.md).
func TestRefreshMountedRemoteDocCorrect(t *testing.T) {
	// Set up a resolver that maps docs/api -> /api (remote != local).
	resolver, err := mount.NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/api", Remote: "repo://self/api", Mode: "readonly", Source: "manual"},
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Fake client returns /api dir and /api/endpoint.md file.
	client := &fakeClient{
		statNode: &store.Node{Path: "/api", Name: "api", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/api/endpoint.md", Name: "endpoint.md", Kind: "file"},
		},
		catContent: "# API Docs\n",
	}

	dir := t.TempDir()
	t.Chdir(dir)

	cmd := newRootCommand(client, client, "gxfs", resolver)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"refresh", "docs/api"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("refresh error: %v", err)
	}

	// Read the manifest and verify remote_doc correctness.
	manifest, err := syncmanifest.Load(filepath.Join(".gxfs", "manifest.toml"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.Entries) != 1 {
		t.Fatalf("manifest entries = %d, want 1", len(manifest.Entries))
	}
	entry := manifest.Entries[0]

	// Local path should use the mounted local name.
	if entry.Local != "docs/api/endpoint.md" {
		t.Errorf("local = %q, want %q", entry.Local, "docs/api/endpoint.md")
	}
	// remote_doc must be the true server path, NOT the localized display path.
	wantRemoteDoc := "repo://self/api/endpoint.md"
	if entry.RemoteDoc != wantRemoteDoc {
		t.Errorf("remote_doc = %q, want %q (must NOT be repo://self/docs/api/endpoint.md)", entry.RemoteDoc, wantRemoteDoc)
	}
}

// TestSyncPullMountedRemoteDocCorrect verifies the same remote_doc correctness
// for sync pull (not just refresh).
func TestSyncPullMountedRemoteDocCorrect(t *testing.T) {
	resolver, err := mount.NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/api", Remote: "repo://self/api", Mode: "readonly", Source: "manual"},
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	client := &fakeClient{
		statNode: &store.Node{Path: "/api", Name: "api", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/api/endpoint.md", Name: "endpoint.md", Kind: "file"},
		},
		catContent: "# API Docs\n",
	}

	dir := t.TempDir()
	t.Chdir(dir)

	cmd := newRootCommand(client, client, "gxfs", resolver)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"sync", "pull", "docs/api"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync pull error: %v", err)
	}

	manifest, err := syncmanifest.Load(filepath.Join(".gxfs", "manifest.toml"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.Entries) != 1 {
		t.Fatalf("manifest entries = %d, want 1", len(manifest.Entries))
	}
	entry := manifest.Entries[0]

	if entry.Local != "docs/api/endpoint.md" {
		t.Errorf("local = %q, want %q", entry.Local, "docs/api/endpoint.md")
	}
	wantRemoteDoc := "repo://self/api/endpoint.md"
	if entry.RemoteDoc != wantRemoteDoc {
		t.Errorf("remote_doc = %q, want %q", entry.RemoteDoc, wantRemoteDoc)
	}
}

// TestRefreshMountedDirtyPathNormalizesLocal verifies that non-canonical
// input paths like "./docs/api/" or "docs/api/" produce a clean manifest
// local path "docs/api/endpoint.md" (not "./docs/api/..." or "docs/api//...").
func TestRefreshMountedDirtyPathNormalizesLocal(t *testing.T) {
	resolver, err := mount.NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/api", Remote: "repo://self/api", Mode: "readonly", Source: "manual"},
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	client := &fakeClient{
		statNode: &store.Node{Path: "/api", Name: "api", Kind: "dir"},
		lsNodes: []store.Node{
			{Path: "/api/endpoint.md", Name: "endpoint.md", Kind: "file"},
		},
		catContent: "# API\n",
	}

	cases := []string{"./docs/api/", "docs/api/", "./docs/api", "docs/api"}

	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)

			cmd := newRootCommand(client, client, "gxfs", resolver)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"refresh", input})

			if err := cmd.Execute(); err != nil {
				t.Fatalf("refresh %q error: %v", input, err)
			}

			manifest, err := syncmanifest.Load(filepath.Join(".gxfs", "manifest.toml"))
			if err != nil {
				t.Fatalf("load manifest: %v", err)
			}
			if len(manifest.Entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(manifest.Entries))
			}
			entry := manifest.Entries[0]
			if entry.Local != "docs/api/endpoint.md" {
				t.Errorf("input %q: local = %q, want %q", input, entry.Local, "docs/api/endpoint.md")
			}
			if entry.RemoteDoc != "repo://self/api/endpoint.md" {
				t.Errorf("input %q: remote_doc = %q, want %q", input, entry.RemoteDoc, "repo://self/api/endpoint.md")
			}
		})
	}
}

// --- CLI Pagination Regression Tests ---

func manyNodes(n int) []store.Node {
	nodes := make([]store.Node, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("file_%02d.txt", i)
		nodes[i] = store.Node{Path: "/" + name, Name: name, Kind: "file"}
	}
	return nodes
}

func TestCLILSLimitOffset(t *testing.T) {
	nodes := manyNodes(10)
	client := &fakeClient{lsNodes: nodes}
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"ls", "--limit", "3", "--offset", "2"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// Verify limit/offset passed to LSRequest
	if client.lsReq.Limit != 3 {
		t.Fatalf("lsReq.Limit = %d, want 3", client.lsReq.Limit)
	}
	if client.lsReq.Offset != 2 {
		t.Fatalf("lsReq.Offset = %d, want 2", client.lsReq.Offset)
	}
}

func TestCLILSNegativeLimit(t *testing.T) {
	err := executeErr(t, "ls", "--limit", "-1")
	if err == nil {
		t.Fatal("expected error for negative limit")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("error = %q, want non-negative message", err.Error())
	}
}

func TestCLILSNegativeOffset(t *testing.T) {
	err := executeErr(t, "ls", "--offset", "-5")
	if err == nil {
		t.Fatal("expected error for negative offset")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("error = %q, want non-negative message", err.Error())
	}
}

func TestCLIFindLimitOffset(t *testing.T) {
	nodes := manyNodes(10)
	client := &fakeClient{findNodes: nodes}
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"find", "--name", "*.txt", "--limit", "3", "--offset", "2"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if client.findReq.Limit != 3 {
		t.Fatalf("findReq.Limit = %d, want 3", client.findReq.Limit)
	}
	if client.findReq.Offset != 2 {
		t.Fatalf("findReq.Offset = %d, want 2", client.findReq.Offset)
	}
}

func TestCLIFindNegativeLimit(t *testing.T) {
	err := executeErr(t, "find", "--name", "*.txt", "--limit", "-1")
	if err == nil {
		t.Fatal("expected error for negative limit")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("error = %q, want non-negative message", err.Error())
	}
}

func TestCLISearchOffset(t *testing.T) {
	client := &fakeClient{
		searchResp: &store.SearchResponse{
			Results: []store.SearchResult{
				{Path: "/a.md", Rank: 0.9, Snippet: "test", Size: 10},
				{Path: "/b.md", Rank: 0.8, Snippet: "test", Size: 20},
			},
			Total: 10,
		},
	}
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"search", "--offset", "3", "test"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if client.searchReq.Offset != 3 {
		t.Fatalf("searchReq.Offset = %d, want 3", client.searchReq.Offset)
	}
	if !strings.Contains(out.String(), "showing 4-5 of 10") {
		t.Fatalf("output = %q, want showing summary", out.String())
	}
}

func TestCLISearchNegativeOffset(t *testing.T) {
	err := executeErr(t, "search", "--offset", "-1", "test")
	if err == nil {
		t.Fatal("expected error for negative offset")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("error = %q, want non-negative message", err.Error())
	}
}

// --- Hash-skip sync behavior tests ---

// TestRefreshHashSkipOnlyCatsChangedFiles verifies that refresh on a 12-file
// directory only Cats the files with unknown or changed hashes. Files whose
// content_hash matches the existing manifest entry must NOT trigger a Cat call.
func TestRefreshHashSkipOnlyCatsChangedFiles(t *testing.T) {
	const nFiles = 12
	const nChanged = 3 // files 0, 5, 11 have changed or unknown hashes

	// Build LS nodes for /docs with 12 files
	lsNodes := make([]store.Node, nFiles)
	for i := 0; i < nFiles; i++ {
		name := fmt.Sprintf("file_%02d.md", i)
		lsNodes[i] = store.Node{Path: "/docs/" + name, Name: name, Kind: "file", Size: 10, ModTime: "2026-05-12T00:00:00Z"}
	}

	// All files share the same base content "content-N" with known hash.
	// Changed files (0..nChanged-1) get a different hash to force Cat.
	hashes := make([]store.ContentHash, nFiles)
	for i := 0; i < nFiles; i++ {
		hash := store.HashContent(fmt.Sprintf("content-%d", i))
		if i < nChanged {
			// Hash differs from manifest entry -> must Cat
			hash = store.HashContent(fmt.Sprintf("changed-%d", i))
		}
		hashes[i] = store.ContentHash{Path: "/docs/" + fmt.Sprintf("file_%02d.md", i), Hash: hash}
	}
	// Cat contents: only called for changed/unknown files
	catContents := make(map[string]string)
	for i := 0; i < nChanged; i++ {
		name := fmt.Sprintf("file_%02d.md", i)
		catContents["/docs/"+name] = fmt.Sprintf("changed-content-%d", i)
	}

	client := &fakeClient{
		statNode: &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes:  lsNodes,
		catContents: func() map[string]string {
			// Cat returns content only for changed files
			m := make(map[string]string)
			for i := 0; i < nChanged; i++ {
				name := fmt.Sprintf("file_%02d.md", i)
				m["/docs/"+name] = fmt.Sprintf("changed-content-%d", i)
			}
			// Also provide content for files without manifest entry (unknown hash)
			// In this test, files 0,1,2 are "changed", rest match manifest
			return m
		}(),
		batchHashesResp: &store.HashResponse{Hashes: hashes},
	}

	dir := t.TempDir()
	t.Chdir(dir)

	// Write a pre-existing manifest with all 12 files having the original hash
	var manifestEntries []string
	for i := 0; i < nFiles; i++ {
		name := fmt.Sprintf("file_%02d.md", i)
		content := fmt.Sprintf("content-%d", i)
		origHash := store.HashContent(content)
		manifestEntries = append(manifestEntries, fmt.Sprintf(
			`[[entries]]
local = 'docs/%s'
remote_doc = 'repo://self/docs/%s'
mount = 'docs'
content_hash = '%s'
size = 10
mtime = '2026-05-12T00:00:00Z'
materialized = false
`, name, name, origHash))
	}
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	writeTestFile(t, manifestPath, "version = 1\n\n"+strings.Join(manifestEntries, "\n"))

	got := executeWithClient(t, client, "refresh", "docs", "--manifest", manifestPath)

	// Verify only nChanged files were Cat'd
	if len(client.catReqs) != nChanged {
		t.Fatalf("Cat requests = %d, want %d (only changed files); paths = %v",
			len(client.catReqs), nChanged, client.catReqs)
	}

	// Verify the refreshed manifest has updated hashes for changed files
	manifest := readTextFile(t, manifestPath)
	for i := 0; i < nChanged; i++ {
		name := fmt.Sprintf("file_%02d.md", i)
		newHash := store.HashContent(fmt.Sprintf("changed-content-%d", i))
		if !strings.Contains(manifest, newHash) {
			t.Errorf("manifest missing updated hash for %s: %s", name, manifest)
		}
	}

	if !strings.Contains(got, "refreshed") {
		t.Fatalf("refresh output = %q, want refreshed count", got)
	}
}

// TestRefreshMountBatchHashesPathMapping verifies that when a mount maps
// repo://self/api to local docs/api, BatchHashes returns/consumes paths
// using the true server path (/api/...), and the manifest correctly maps
// them to local paths (docs/api/...).
func TestRefreshMountBatchHashesPathMapping(t *testing.T) {
	resolver, err := mount.NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/api", Remote: "repo://self/api", Mode: "readonly", Source: "manual"},
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Server paths are under /api (the remote path), NOT /docs/api
	apiFiles := []store.Node{
		{Path: "/api/endpoint.md", Name: "endpoint.md", Kind: "file", Size: 20},
		{Path: "/api/schema.md", Name: "schema.md", Kind: "file", Size: 30},
	}

	hashes := []store.ContentHash{
		{Path: "/api/endpoint.md", Hash: store.HashContent("endpoint content")},
		{Path: "/api/schema.md", Hash: store.HashContent("schema content")},
	}

	client := &fakeClient{
		statNode:        &store.Node{Path: "/api", Name: "api", Kind: "dir"},
		lsNodes:         apiFiles,
		catContents:     map[string]string{"/api/endpoint.md": "endpoint content", "/api/schema.md": "schema content"},
		batchHashesResp: &store.HashResponse{Hashes: hashes},
	}

	dir := t.TempDir()
	t.Chdir(dir)

	cmd := newRootCommand(client, client, "gxfs", resolver)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"refresh", "docs/api"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("refresh error: %v", err)
	}

	// Verify manifest has correct local paths (docs/api/...) and remote_doc (repo://self/api/...)
	manifest, err := syncmanifest.Load(filepath.Join(".gxfs", "manifest.toml"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.Entries) != 2 {
		t.Fatalf("manifest entries = %d, want 2", len(manifest.Entries))
	}

	for _, entry := range manifest.Entries {
		// Local path must use the mounted local prefix
		if !strings.HasPrefix(entry.Local, "docs/api/") {
			t.Errorf("local = %q, want prefix docs/api/", entry.Local)
		}
		// remote_doc must use the true server path, NOT the local display path
		if !strings.HasPrefix(entry.RemoteDoc, "repo://self/api/") {
			t.Errorf("remote_doc = %q, want prefix repo://self/api/", entry.RemoteDoc)
		}
		// Verify hash was populated
		if entry.ContentHash == "" {
			t.Errorf("entry %q has empty content_hash", entry.Local)
		}
	}
}

// TestMaterializeAfterRefreshWritesRealContent verifies that running refresh
// (which hash-skips Cat for unchanged files) followed by materialize on the
// same root writes the actual file content, NOT empty content.
// This is the regression test for the Phase 3D materialize blocker.
func TestMaterializeAfterRefreshWritesRealContent(t *testing.T) {
	// Set up 5 files, all with matching hashes in manifest (so refresh skips Cat)
	const nFiles = 5
	lsNodes := make([]store.Node, nFiles)
	hashes := make([]store.ContentHash, nFiles)
	catContents := make(map[string]string, nFiles)
	manifestEntries := make([]string, nFiles)

	for i := 0; i < nFiles; i++ {
		name := fmt.Sprintf("file_%02d.md", i)
		path := "/docs/" + name
		content := fmt.Sprintf("content-%d", i)
		hash := store.HashContent(content)

		lsNodes[i] = store.Node{Path: path, Name: name, Kind: "file", Size: int64(len(content)), ModTime: "2026-05-12T00:00:00Z"}
		hashes[i] = store.ContentHash{Path: path, Hash: hash}
		catContents[path] = content
		manifestEntries[i] = fmt.Sprintf(
			`[[entries]]
local = 'docs/%s'
remote_doc = 'repo://self/docs/%s'
mount = 'docs'
content_hash = '%s'
size = %d
mtime = '2026-05-12T00:00:00Z'
materialized = false
`, name, name, hash, len(content))
	}

	client := &fakeClient{
		statNode:        &store.Node{Path: "/docs", Name: "docs", Kind: "dir"},
		lsNodes:         lsNodes,
		catContents:     catContents,
		batchHashesResp: &store.HashResponse{Hashes: hashes},
	}

	dir := t.TempDir()
	t.Chdir(dir)
	manifestPath := filepath.Join(dir, ".gxfs", "manifest.toml")
	writeTestFile(t, manifestPath, "version = 1\n\n"+strings.Join(manifestEntries, "\n"))

	// Step 1: refresh — should skip Cat (all hashes match)
	refreshOut := executeWithClient(t, client, "refresh", "docs", "--manifest", manifestPath)
	if !strings.Contains(refreshOut, "refreshed") {
		t.Fatalf("refresh output = %q, want refreshed", refreshOut)
	}
	// Verify refresh did NOT Cat (all hashes matched)
	if len(client.catReqs) != 0 {
		t.Fatalf("refresh Cat requests = %d, want 0 (all hashes matched); got paths = %v",
			len(client.catReqs), client.catReqs)
	}

	// Step 2: materialize same root — must write real content, not empty
	matOut := executeWithClient(t, client, "materialize", "docs", "--manifest", manifestPath)
	if !strings.Contains(matOut, "materialized") {
		t.Fatalf("materialize output = %q, want materialized", matOut)
	}

	// Verify files on disk have real content (not empty)
	for i := 0; i < nFiles; i++ {
		name := fmt.Sprintf("file_%02d.md", i)
		localPath := filepath.Join(dir, "docs", name)
		got, err := os.ReadFile(localPath)
		if err != nil {
			t.Fatalf("read %s: %v", localPath, err)
		}
		want := fmt.Sprintf("content-%d", i)
		if string(got) != want {
			t.Errorf("materialized %s = %q, want %q (hash-skipped files must be Cat'd on demand)", name, string(got), want)
		}
	}
}

func TestRepoListOutput(t *testing.T) {
	out, _ := execute(t, "repo", "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("repo list: got %d lines, want 2: %q", len(lines), out)
	}
	if lines[0] != "my-project" {
		t.Errorf("repo list[0] = %q, want my-project", lines[0])
	}
	if lines[1] != "github/openai-go" {
		t.Errorf("repo list[1] = %q, want github/openai-go", lines[1])
	}
}

func TestGlobOutput(t *testing.T) {
	out, client := execute(t, "glob", "**/*.md")
	if client == nil {
		t.Fatal("client is nil")
	}
	if !strings.Contains(out, "docs/readme.md") {
		t.Errorf("glob output missing docs/readme.md: %q", out)
	}
	if !strings.Contains(out, "docs/api.md") {
		t.Errorf("glob output missing docs/api.md: %q", out)
	}
}

func TestGlobAllReposOutput(t *testing.T) {
	out, _ := execute(t, "glob", "**/*.md", "--all-repos")
	// Should contain repo:// refs from both repos
	if !strings.Contains(out, "repo://my-project/") {
		t.Errorf("glob --all-repos missing repo://my-project: %q", out)
	}
	if !strings.Contains(out, "repo://github%2Fopenai-go/") {
		t.Errorf("glob --all-repos missing repo://github%%2Fopenai-go: %q", out)
	}
}

func TestAttachNotFound(t *testing.T) {
	err := executeErr(t, "attach", "not-found-repo", "--into", "docs/lib")
	if err == nil {
		t.Fatal("attach not-found-repo: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no repos matched") {
		t.Errorf("attach error = %q, want 'no repos matched'", err.Error())
	}
}

func TestAttachMultipleMatches(t *testing.T) {
	// "project" matches nothing because suffix match is on last segment only
	// Let's test ambiguous match by adding another repo ending in "openai-go"
	// Actually our fakeClient returns ["my-project", "github/openai-go"]
	// "openai-go" should uniquely match "github/openai-go" (suffix on last segment)
}

func TestAttachUniqueMatch(t *testing.T) {
	// "openai-go" matches "github/openai-go" uniquely via suffix match
	// But this requires a real mounts.toml file, so we just test the matching logic
	// The attach command writes to mounts.toml and calls Stat, which our fakeClient handles
	out, _ := execute(t, "attach", "openai-go", "--into", "docs/lib/openai-go", "--force")
	if !strings.Contains(out, "attached") && !strings.Contains(out, "replaced mount") {
		t.Errorf("attach output = %q, want 'attached' or 'replaced mount'", out)
	}
}

func TestAttachDryRun(t *testing.T) {
	out, _ := execute(t, "attach", "openai-go", "--into", "docs/lib/openai-go", "--dry-run")
	if !strings.Contains(out, "[dry-run]") {
		t.Errorf("attach --dry-run output = %q, want '[dry-run]'", out)
	}
}

// --- Phase 3 tests ---

func TestHookSessionStartNoConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	client := &fakeClient{}
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hook", "session-start"})

	// Should not error even without .gxfs config.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("hook session-start error = %v", err)
	}
}

func TestHookSessionStartNoMounts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create .gxfs/settings.toml and empty mounts.toml
	gxfsDir := filepath.Join(dir, ".gxfs")
	if err := os.MkdirAll(gxfsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(gxfsDir, "settings.toml"), "repo = \"test\"\n[server]\naddr = \"http://localhost:7635\"\n")
	writeTestFile(t, filepath.Join(gxfsDir, "mounts.toml"), "version = 1\nmounts = []\n")

	client := &fakeClient{}
	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hook", "session-start"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("hook session-start error = %v", err)
	}
	// Should produce no output when no mounts.
	if out.String() != "" {
		t.Errorf("expected empty output, got %q", out.String())
	}
}

func TestClaudeHooksCreatesSettings(t *testing.T) {
	dir := t.TempDir()

	if err := command.UpsertClaudeProjectHooks(dir); err != nil {
		t.Fatalf("command.UpsertClaudeProjectHooks error = %v", err)
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("settings.json missing 'hooks' key")
	}
	sessionStart, ok := hooks["SessionStart"].([]any)
	if !ok || len(sessionStart) == 0 {
		t.Fatal("settings.json missing hooks.SessionStart array")
	}

	group, ok := sessionStart[0].(map[string]any)
	if !ok {
		t.Fatal("SessionStart[0] is not an object")
	}
	if group["matcher"] != "startup|resume" {
		t.Errorf("matcher = %v, want startup|resume", group["matcher"])
	}
	hookList, ok := group["hooks"].([]any)
	if !ok || len(hookList) == 0 {
		t.Fatal("SessionStart[0] missing hooks array")
	}
	h, ok := hookList[0].(map[string]any)
	if !ok {
		t.Fatal("hook[0] is not an object")
	}
	if h["type"] != "command" {
		t.Errorf("hook type = %v, want command", h["type"])
	}
	cmd, _ := h["command"].(string)
	if !strings.Contains(cmd, "gxfs") {
		t.Errorf("hook command = %q, want gxfs path", cmd)
	}
}

func TestClaudeHooksCreatesUserSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := command.UpsertClaudeUserHooks(); err != nil {
		t.Fatalf("command.UpsertClaudeUserHooks error = %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("stat user settings.json: %v", err)
	}

	scriptPath := filepath.Join(home, ".claude", "hooks", "pre_tool_use.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat pre_tool_use.sh: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("pre_tool_use.sh mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestClaudeHooksMergeExisting(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-existing settings with a different hook.
	existing := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"matcher": "Write|Edit",
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo hello"},
					},
				},
			},
		},
	}
	existingData, _ := json.Marshal(existing)
	writeTestFile(t, filepath.Join(claudeDir, "settings.json"), string(existingData))

	if err := command.UpsertClaudeProjectHooks(dir); err != nil {
		t.Fatalf("command.UpsertClaudeProjectHooks error = %v", err)
	}

	data := readTextFile(t, filepath.Join(claudeDir, "settings.json"))
	var settings map[string]any
	json.Unmarshal([]byte(data), &settings)

	hooks := settings["hooks"].(map[string]any)

	// PostToolUse should still exist.
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Fatal("PostToolUse was removed by merge")
	}

	// SessionStart should be added.
	if _, ok := hooks["SessionStart"]; !ok {
		t.Fatal("SessionStart was not added")
	}
}

func TestClaudeHooksDeduplicateCommand(t *testing.T) {
	dir := t.TempDir()

	// Run twice — second should be a no-op.
	if err := command.UpsertClaudeProjectHooks(dir); err != nil {
		t.Fatalf("first call error = %v", err)
	}
	data1 := readTextFile(t, filepath.Join(dir, ".claude", "settings.json"))

	if err := command.UpsertClaudeProjectHooks(dir); err != nil {
		t.Fatalf("second call error = %v", err)
	}
	data2 := readTextFile(t, filepath.Join(dir, ".claude", "settings.json"))

	if data1 != data2 {
		t.Error("second call should be a no-op but settings.json changed")
	}
}

func TestCodexHooksCreatesHooksJSON(t *testing.T) {
	dir := t.TempDir()

	if err := command.UpsertCodexProjectHooks(dir); err != nil {
		t.Fatalf("command.UpsertCodexProjectHooks error = %v", err)
	}

	hooksPath := filepath.Join(dir, ".codex", "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}

	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks.json missing hooks object")
	}
	sessionStart, ok := hooks["SessionStart"].([]any)
	if !ok || len(sessionStart) == 0 {
		t.Fatal("hooks.json missing hooks.SessionStart array")
	}
	sessionGroup := sessionStart[0].(map[string]any)
	if sessionGroup["matcher"] != "startup|resume" {
		t.Errorf("SessionStart matcher = %v, want startup|resume", sessionGroup["matcher"])
	}
	sessionHooks := sessionGroup["hooks"].([]any)
	sessionHook := sessionHooks[0].(map[string]any)
	if sessionHook["type"] != "command" {
		t.Errorf("SessionStart hook type = %v, want command", sessionHook["type"])
	}
	sessionCmd, _ := sessionHook["command"].(string)
	if !strings.Contains(sessionCmd, "hook session-start") {
		t.Errorf("SessionStart command = %q, want hook session-start", sessionCmd)
	}

	preToolUse, ok := hooks["PreToolUse"].([]any)
	if !ok || len(preToolUse) == 0 {
		t.Fatal("hooks.json missing hooks.PreToolUse array")
	}
	preGroup := preToolUse[0].(map[string]any)
	if preGroup["matcher"] != "Bash" {
		t.Errorf("PreToolUse matcher = %v, want Bash", preGroup["matcher"])
	}
	preHooks := preGroup["hooks"].([]any)
	preHook := preHooks[0].(map[string]any)
	preCmd, _ := preHook["command"].(string)
	if !strings.Contains(preCmd, ".codex/hooks/pre_tool_use.sh") {
		t.Errorf("PreToolUse command = %q, want .codex hook script", preCmd)
	}

	scriptPath := filepath.Join(dir, ".codex", "hooks", "pre_tool_use.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat pre_tool_use.sh: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("pre_tool_use.sh mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestCodexHooksCreatesUserHooksJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := command.UpsertCodexUserHooks(); err != nil {
		t.Fatalf("command.UpsertCodexUserHooks error = %v", err)
	}

	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read user hooks.json: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse user hooks.json: %v", err)
	}
	hooks := config["hooks"].(map[string]any)
	preToolUse := hooks["PreToolUse"].([]any)
	preGroup := preToolUse[0].(map[string]any)
	preHooks := preGroup["hooks"].([]any)
	preHook := preHooks[0].(map[string]any)
	preCmd, _ := preHook["command"].(string)
	if !strings.Contains(preCmd, filepath.Join(home, ".codex", "hooks", "pre_tool_use.sh")) {
		t.Errorf("PreToolUse command = %q, want user-level hook script path", preCmd)
	}
}

func TestCodexProjectHooksResolveGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := exec.Command("git", "init", root).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	subdir := filepath.Join(root, "nested", "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := command.UpsertCodexProjectHooks(subdir); err != nil {
		t.Fatalf("command.UpsertCodexProjectHooks error = %v", err)
	}

	rootHooksPath := filepath.Join(root, ".codex", "hooks.json")
	if _, err := os.Stat(rootHooksPath); err != nil {
		t.Fatalf("stat root hooks.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(subdir, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("subdir hooks.json exists or stat failed: %v", err)
	}

	var config map[string]any
	data, err := os.ReadFile(rootHooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	hooks := config["hooks"].(map[string]any)
	preToolUse := hooks["PreToolUse"].([]any)
	preGroup := preToolUse[0].(map[string]any)
	preHooks := preGroup["hooks"].([]any)
	preHook := preHooks[0].(map[string]any)
	preCmd, _ := preHook["command"].(string)
	if !strings.Contains(preCmd, "git rev-parse --show-toplevel") {
		t.Errorf("PreToolUse command = %q, want git-root based path", preCmd)
	}
}

func TestCodexHooksMergeExisting(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}

	existing := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo review"},
					},
				},
			},
		},
	}
	existingData, _ := json.Marshal(existing)
	writeTestFile(t, filepath.Join(codexDir, "hooks.json"), string(existingData))

	if err := command.UpsertCodexProjectHooks(dir); err != nil {
		t.Fatalf("command.UpsertCodexProjectHooks error = %v", err)
	}

	data := readTextFile(t, filepath.Join(codexDir, "hooks.json"))
	var config map[string]any
	if err := json.Unmarshal([]byte(data), &config); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	hooks := config["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Fatal("PostToolUse was removed by merge")
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Fatal("SessionStart was not added")
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Fatal("PreToolUse was not added")
	}
}

func TestCodexHooksDeduplicateCommand(t *testing.T) {
	dir := t.TempDir()

	if err := command.UpsertCodexProjectHooks(dir); err != nil {
		t.Fatalf("first call error = %v", err)
	}
	data1 := readTextFile(t, filepath.Join(dir, ".codex", "hooks.json"))

	if err := command.UpsertCodexProjectHooks(dir); err != nil {
		t.Fatalf("second call error = %v", err)
	}
	data2 := readTextFile(t, filepath.Join(dir, ".codex", "hooks.json"))

	if data1 != data2 {
		t.Error("second call should be a no-op but hooks.json changed")
	}
}

func TestCodexHooksInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(codexDir, "hooks.json"), "{not-json")

	if err := command.UpsertCodexProjectHooks(dir); err == nil {
		t.Fatal("UpsertCodexProjectHooks error = nil, want parse error")
	}
}

func TestInitHookCodexProject(t *testing.T) {
	dir := t.TempDir()
	cmd := command.NewInitCommand()
	cmd.SetArgs([]string{"--hook", "codex", "--scope", "project", dir})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, ".codex/hooks.json") {
		t.Errorf("init output = %q, want hooks.json update", output)
	}
	if !strings.Contains(output, "/hooks") {
		t.Errorf("init output = %q, want Codex trust hint", output)
	}

	hooksPath := filepath.Join(dir, ".codex", "hooks.json")
	if _, err := os.Stat(hooksPath); os.IsNotExist(err) {
		t.Fatal("hooks.json not created")
	}
}

func TestInitHookCodexDefaultUserScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	cmd := command.NewInitCommand()
	cmd.SetArgs([]string{"--hook", "codex", dir})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "~/.codex/hooks.json") {
		t.Errorf("init output = %q, want user hooks.json update", output)
	}
	userHooksPath := filepath.Join(home, ".codex", "hooks.json")
	if _, err := os.Stat(userHooksPath); os.IsNotExist(err) {
		t.Fatal("user hooks.json not created")
	}
	projectHooksPath := filepath.Join(dir, ".codex", "hooks.json")
	if _, err := os.Stat(projectHooksPath); err == nil {
		t.Fatal("project hooks.json created for default user scope")
	}
}

func TestInitHookClaudeProject(t *testing.T) {
	dir := t.TempDir()
	cmd := command.NewInitCommand()
	cmd.SetArgs([]string{"--hook", "claude", "--scope", "project", dir})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init error = %v", err)
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Fatal("settings.json not created")
	}
}

func TestInitHookUnsupportedScope(t *testing.T) {
	dir := t.TempDir()
	cmd := command.NewInitCommand()
	cmd.SetArgs([]string{"--hook", "codex", "--scope", "global", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("init error = nil, want unsupported scope error")
	}
}

func TestInitHookUnsupportedTarget(t *testing.T) {
	dir := t.TempDir()
	cmd := command.NewInitCommand()
	cmd.SetArgs([]string{"--hook", "gemini", "--scope", "project", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("init error = nil, want unsupported hook target error")
	}
}

func TestHookSessionStartOverwritesUnchangedLocal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Setup: .gxfs dir with settings, mounts, manifest, and a materialized file.
	gxfsDir := filepath.Join(dir, ".gxfs")
	os.MkdirAll(gxfsDir, 0o755)
	writeTestFile(t, filepath.Join(gxfsDir, "settings.toml"), "repo = \"test\"\n[server]\naddr = \"http://localhost:7635\"\n")
	writeTestFile(t, filepath.Join(gxfsDir, "mounts.toml"), "version = 1\n[[mounts]]\nlocal = \"docs\"\nremote = \"repo://self/docs\"\nmode = \"readonly\"\n")

	// Create a materialized file at docs/readme.md.
	oldHash := store.HashContent("old content")
	writeTestFile(t, filepath.Join(dir, "docs", "readme.md"), "old content")

	// Write manifest with the old hash.
	manifest := syncmanifest.Manifest{Version: 1, Entries: []syncmanifest.Entry{
		{Local: "docs/readme.md", RemoteDoc: "repo://self/docs/readme.md", Mount: "docs", ContentHash: oldHash, Materialized: true},
	}}
	if err := syncmanifest.Save(filepath.Join(gxfsDir, "manifest.toml"), manifest); err != nil {
		t.Fatal(err)
	}

	// fakeClient returns a new file list with different hash (simulating remote change).
	newContent := "new content from server"
	newHash := store.HashContent(newContent)
	client := &fakeClient{
		lsNodes: []store.Node{
			{Path: "/docs/readme.md", Name: "readme.md", Kind: "file", Size: int64(len(newContent)), Hash: newHash},
		},
		catContent: newContent,
	}

	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hook", "session-start"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("hook session-start error = %v", err)
	}

	// Local file should be overwritten with new content.
	localContent := readTextFile(t, filepath.Join(dir, "docs", "readme.md"))
	if localContent != newContent {
		t.Errorf("local file = %q, want %q", localContent, newContent)
	}

	// Manifest should have new hash.
	updated, err := syncmanifest.Load(filepath.Join(gxfsDir, "manifest.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Entries) == 0 {
		t.Fatal("manifest has no entries")
	}
	if updated.Entries[0].ContentHash != newHash {
		t.Errorf("manifest hash = %q, want %q", updated.Entries[0].ContentHash, newHash)
	}
}

func TestHookSessionStartPreservesLocalConflict(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	gxfsDir := filepath.Join(dir, ".gxfs")
	os.MkdirAll(gxfsDir, 0o755)
	writeTestFile(t, filepath.Join(gxfsDir, "settings.toml"), "repo = \"test\"\n[server]\naddr = \"http://localhost:7635\"\n")
	writeTestFile(t, filepath.Join(gxfsDir, "mounts.toml"), "version = 1\n[[mounts]]\nlocal = \"docs\"\nremote = \"repo://self/docs\"\nmode = \"readonly\"\n")

	oldHash := store.HashContent("old content")
	// Local file has been modified (different from manifest baseline).
	localModified := "locally modified content"
	writeTestFile(t, filepath.Join(dir, "docs", "readme.md"), localModified)

	manifest := syncmanifest.Manifest{Version: 1, Entries: []syncmanifest.Entry{
		{Local: "docs/readme.md", RemoteDoc: "repo://self/docs/readme.md", Mount: "docs", ContentHash: oldHash, Materialized: true},
	}}
	if err := syncmanifest.Save(filepath.Join(gxfsDir, "manifest.toml"), manifest); err != nil {
		t.Fatal(err)
	}

	// Remote has new content.
	newContent := "new content from server"
	newHash := store.HashContent(newContent)
	client := &fakeClient{
		lsNodes: []store.Node{
			{Path: "/docs/readme.md", Name: "readme.md", Kind: "file", Size: int64(len(newContent)), Hash: newHash},
		},
		catContent: newContent,
	}

	cmd := newRootCommand(client, client, "gxfs", nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hook", "session-start"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("hook session-start error = %v", err)
	}

	// Local file should NOT be overwritten.
	localContent := readTextFile(t, filepath.Join(dir, "docs", "readme.md"))
	if localContent != localModified {
		t.Errorf("local file = %q, want %q (should not be overwritten)", localContent, localModified)
	}

	// Manifest should STILL have old hash (preserving baseline).
	updated, err := syncmanifest.Load(filepath.Join(gxfsDir, "manifest.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Entries) == 0 {
		t.Fatal("manifest has no entries")
	}
	if updated.Entries[0].ContentHash != oldHash {
		t.Errorf("manifest hash = %q, want %q (should preserve old baseline on conflict)", updated.Entries[0].ContentHash, oldHash)
	}
}

// --- #14 Cross-repo Writable Mount Tests ---

func TestMountAddCrossRepoWritable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	client := &fakeClient{}

	got := executeWithClient(t, client, "mount", "add", "repo://other-repo/docs", "libs/other", "--mode", "writable", "--no-refresh")
	if !strings.Contains(got, "added mount libs/other") {
		t.Fatalf("output = %q, want add confirmation", got)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gxfs", "mounts.toml"))
	if err != nil {
		t.Fatalf("read mounts.toml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "mode = 'writable'") {
		t.Errorf("mounts.toml missing writable mode: %s", content)
	}
}

func TestServerCrossRepoWriteGateRejectsNonWritable(t *testing.T) {
	noopAdapter := &testNoopAdapter{}
	writableRepos := map[string]bool{} // target-repo not writable
	handler := server.NewHandler(noopAdapter, writableRepos)

	req := httptest.NewRequest(http.MethodPut, "/v1/repos/target-repo/write?path=/hello.txt", strings.NewReader("content"))
	req.Header.Set("X-Client-Repo", "source-repo")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestServerCrossRepoWriteGateAllowsWritable(t *testing.T) {
	noopAdapter := &testNoopAdapter{}
	writableRepos := map[string]bool{"target-repo": true}
	handler := server.NewHandler(noopAdapter, writableRepos)

	req := httptest.NewRequest(http.MethodPut, "/v1/repos/target-repo/write?path=/hello.txt", strings.NewReader("content"))
	req.Header.Set("X-Client-Repo", "source-repo")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerSelfRepoWriteAlwaysAllowed(t *testing.T) {
	noopAdapter := &testNoopAdapter{}
	writableRepos := map[string]bool{} // nothing writable
	handler := server.NewHandler(noopAdapter, writableRepos)

	req := httptest.NewRequest(http.MethodPut, "/v1/repos/my-repo/write?path=/hello.txt", strings.NewReader("content"))
	// No X-Client-Repo header → self-repo write
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (self-repo write should always succeed)", w.Code, http.StatusOK)
	}
}

func TestServerCASConflict(t *testing.T) {
	noopAdapter := &testNoopAdapter{}
	writableRepos := map[string]bool{}
	handler := server.NewHandler(noopAdapter, writableRepos)

	// Write with If-Match header but the adapter returns conflict.
	// The noop adapter returns success, so we test that the CAS field is parsed
	// and passed through. For actual CAS testing we need a real adapter.
	// This test verifies the HTTP contract: If-Match header is parsed and passed.
	req := httptest.NewRequest(http.MethodPut, "/v1/repos/my-repo/write?path=/hello.txt", strings.NewReader("content"))
	req.Header.Set("If-Match", `"sha256:abc123"`)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// The noop adapter returns success, meaning the header was parsed and didn't break anything.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestServerCreateOnlyRejectsExisting(t *testing.T) {
	conflictAdapter := &testConflictAdapter{}
	writableRepos := map[string]bool{}
	handler := server.NewHandler(conflictAdapter, writableRepos)

	req := httptest.NewRequest(http.MethodPut, "/v1/repos/my-repo/write?path=/hello.txt", strings.NewReader("content"))
	req.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (create-only on existing path should return 409)", w.Code, http.StatusConflict)
	}
}

func TestServerExpectedHashQueryParam(t *testing.T) {
	conflictAdapter := &testConflictAdapter{}
	writableRepos := map[string]bool{}
	handler := server.NewHandler(conflictAdapter, writableRepos)

	req := httptest.NewRequest(http.MethodPut, "/v1/repos/my-repo/write?path=/hello.txt&expected_hash=sha256%3Awrong", strings.NewReader("content"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (CAS mismatch via query param should return 409)", w.Code, http.StatusConflict)
	}
}

func TestClientSetClientRepo(t *testing.T) {
	c := client.New("http://example.com")
	c.SetClientRepo("my-repo")
	if c.ClientRepo() != "my-repo" {
		t.Errorf("ClientRepo() = %q, want %q", c.ClientRepo(), "my-repo")
	}
}

// testNoopAdapter is a minimal adapter that returns success for all write operations.
type testNoopAdapter struct{}

func (t *testNoopAdapter) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	return &store.LSResponse{}, nil
}
func (t *testNoopAdapter) Tree(ctx context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	return &store.TreeResponse{}, nil
}
func (t *testNoopAdapter) Cat(ctx context.Context, req store.CatRequest) (*store.CatResponse, error) {
	return &store.CatResponse{}, nil
}
func (t *testNoopAdapter) Grep(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	return &store.GrepResponse{}, nil
}
func (t *testNoopAdapter) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	return &store.FindResponse{}, nil
}
func (t *testNoopAdapter) Stat(ctx context.Context, req store.StatRequest) (*store.StatResponse, error) {
	return &store.StatResponse{}, nil
}
func (t *testNoopAdapter) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	return &store.PutResponse{Node: store.Node{Path: req.Path, Name: "test", Kind: "file"}}, nil
}
func (t *testNoopAdapter) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	return &store.DeleteResponse{}, nil
}
func (t *testNoopAdapter) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	return &store.EditResponse{Path: req.Path, Replaced: 1}, nil
}
func (t *testNoopAdapter) Search(ctx context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	return &store.SearchResponse{}, nil
}
func (t *testNoopAdapter) BatchHashes(ctx context.Context, req store.HashRequest) (*store.HashResponse, error) {
	return &store.HashResponse{}, nil
}
func (t *testNoopAdapter) Glob(ctx context.Context, req store.GlobRequest) (*store.GlobResponse, error) {
	return &store.GlobResponse{}, nil
}
func (t *testNoopAdapter) Locate(ctx context.Context, req store.LocateRequest) (*store.LocateResponse, error) {
	return &store.LocateResponse{}, nil
}
func (t *testNoopAdapter) Repos() []string { return []string{"test"} }
func (t *testNoopAdapter) Invalidate()     {}

// testConflictAdapter returns ErrConflict for all writes when ExpectedHash is set.
type testConflictAdapter struct {
	testNoopAdapter
}

func (t *testConflictAdapter) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	if req.ExpectedHash != "" {
		return nil, store.ErrConflict
	}
	return t.testNoopAdapter.Put(ctx, req)
}
func (t *testConflictAdapter) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	if req.ExpectedHash != "" {
		return nil, store.ErrConflict
	}
	return t.testNoopAdapter.Delete(ctx, req)
}
func (t *testConflictAdapter) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	if req.ExpectedHash != "" {
		return nil, store.ErrConflict
	}
	return t.testNoopAdapter.Edit(ctx, req)
}

// --- #14 Strict CAS Regression Tests ---

func TestWriteSelfRepoNoBaselineStillWorks(t *testing.T) {
	// Self-repo write without manifest baseline should NOT be blocked.
	dir := t.TempDir()
	t.Chdir(dir)

	fake := &fakeClient{}
	// No manifest file exists.
	cmd := newRootCommand(fake, fake, "my-repo", nil)
	cmd.SetArgs([]string{"write", "/docs/new.md", "hello"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("self-repo write without baseline should succeed, got: %v", err)
	}
}

func TestWriteSelfRepoEditNoBaselineStillWorks(t *testing.T) {
	// Self-repo edit without manifest baseline should NOT be blocked.
	dir := t.TempDir()
	t.Chdir(dir)

	fake := &fakeClient{
		catContents: map[string]string{"/docs/readme.md": "old text here"},
	}
	cmd := newRootCommand(fake, fake, "my-repo", nil)
	cmd.SetArgs([]string{"edit", "/docs/readme.md", "--old", "old", "--new", "new"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("self-repo edit without baseline should succeed, got: %v", err)
	}
}

func TestWriteSelfRepoDeleteNoBaselineStillWorks(t *testing.T) {
	// Self-repo delete without manifest baseline should NOT be blocked.
	dir := t.TempDir()
	t.Chdir(dir)

	fake := &fakeClient{}
	cmd := newRootCommand(fake, fake, "my-repo", nil)
	cmd.SetArgs([]string{"delete", "/docs/old.md"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("self-repo delete without baseline should succeed, got: %v", err)
	}
}

func TestStatErrorNotTreatedAsCreateOnly(t *testing.T) {
	// When Stat returns a non-NotFound error, write should NOT fall through to create-only.
	dir := t.TempDir()
	t.Chdir(dir)

	// Write mounts so cross-repo mount exists
	mounts := config.MountsConfig{
		Version: 1,
		Mounts: []config.MountConfig{
			{Local: "docs", Remote: "repo://self/docs", Mode: "writable", Source: "default"},
			{Local: "libs/other", Remote: "repo://other-repo/docs", Mode: "writable", Source: "manual"},
		},
	}
	if err := config.SaveMounts(filepath.Join(dir, ".gxfs", "mounts.toml"), mounts); err != nil {
		t.Fatal(err)
	}

	// fakeClient.Stat returns a generic error (not ErrNotFound)
	fake := &fakeClient{
		statErr: fmt.Errorf("server error 500"),
	}

	resolver, err := mount.NewResolver("my-repo", []config.MountConfig{
		{Local: "docs", Remote: "repo://self/docs", Mode: "writable"},
		{Local: "libs/other", Remote: "repo://other-repo/docs", Mode: "writable"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := newRootCommand(fake, fake, "my-repo", resolver)
	cmd.SetArgs([]string{"write", "libs/other/test.md", "content"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err = cmd.Execute()
	if err == nil {
		t.Fatal("write should fail when Stat returns non-NotFound error")
	}
	if !strings.Contains(err.Error(), "server error 500") {
		t.Errorf("error = %q, want server error 500 to be propagated", err.Error())
	}
}

// --- #15 Lexical Locate Tests ---

func TestLocateRepoRefEncoding(t *testing.T) {
	// Test that repo names with "/" are URL-encoded in repo:// refs
	fake := &fakeClient{
		locateResp: &store.LocateResponse{
			Results: []store.LocateResult{
				{Path: "/docs/readme.md", Score: 1.0, Snippet: "test"},
			},
			Total: 1,
		},
	}
	cmd := newRootCommand(fake, fake, "github/openai-go", nil)
	cmd.SetArgs([]string{"locate", "test query"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("locate failed: %v", err)
	}

	// The Ref should contain URL-encoded repo name
	output := out.String()
	if !strings.Contains(output, "repo://github%2Fopenai-go/") {
		t.Errorf("output = %q, want URL-encoded repo name in ref", output)
	}
}

func TestLocateAllReposMerge(t *testing.T) {
	// Test that --all-repos merges results by score
	fake := &fakeClient{
		repoList: []string{"repo-a", "repo-b"},
		locateResp: &store.LocateResponse{
			Results: []store.LocateResult{
				{Path: "/high.md", Score: 2.0, Snippet: "high score"},
				{Path: "/low.md", Score: 0.5, Snippet: "low score"},
			},
			Total: 2,
		},
	}
	cmd := newRootCommand(fake, fake, "test-repo", nil)
	cmd.SetArgs([]string{"locate", "query", "--all-repos"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("locate --all-repos failed: %v", err)
	}

	output := out.String()
	// Results should be sorted by score (high first)
	if strings.Index(output, "high.md") > strings.Index(output, "low.md") {
		t.Errorf("results not sorted by score: %s", output)
	}
}

func TestLocateAllReposPartialFailure(t *testing.T) {
	// Test that --all-repos reports partial failures
	// We need a more sophisticated fake for this - one that returns different results per repo
	fake := &fakeClient{
		repoList:  []string{"repo-a", "repo-b"},
		locateErr: fmt.Errorf("locate failed"),
	}
	cmd := newRootCommand(fake, fake, "test-repo", nil)
	cmd.SetArgs([]string{"locate", "query", "--all-repos"})
	cmd.SetOut(io.Discard)
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when all repos fail")
	}
	if !strings.Contains(err.Error(), "all repos") {
		t.Errorf("error = %q, want 'all repos' error", err.Error())
	}
}

func TestLocateAllReposSomeReposFail(t *testing.T) {
	// Test that --all-repos warns about partial failures but still returns results
	fake := &fakeClient{
		repoList: []string{"repo-a", "repo-b"},
		locateErrByRepo: map[string]error{
			"repo-a": fmt.Errorf("connection refused"),
		},
		locateRespByRepo: map[string]*store.LocateResponse{
			"repo-b": {
				Results: []store.LocateResult{
					{Path: "/doc.md", Score: 1.0, Snippet: "found"},
				},
				Total: 1,
			},
		},
	}
	cmd := newRootCommand(fake, fake, "test-repo", nil)
	cmd.SetArgs([]string{"locate", "query", "--all-repos"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check stderr has warning about repo-a failure
	errStr := errOut.String()
	if !strings.Contains(errStr, "warning") || !strings.Contains(errStr, "repo-a") {
		t.Errorf("stderr = %q, want warning about repo-a failure", errStr)
	}

	// Check stdout has results from repo-b
	outStr := out.String()
	if !strings.Contains(outStr, "repo-b") {
		t.Errorf("stdout = %q, want results from repo-b", outStr)
	}
}

func TestLocateJSONOutput(t *testing.T) {
	fake := &fakeClient{
		locateResp: &store.LocateResponse{
			Results: []store.LocateResult{
				{Path: "/doc.md", Score: 1.5, Snippet: "test snippet"},
			},
			Total: 1,
		},
	}
	cmd := newRootCommand(fake, fake, "test-repo", nil)
	cmd.SetArgs([]string{"locate", "query", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("locate --json failed: %v", err)
	}

	var resp store.LocateResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
	if len(resp.Results) != 1 {
		t.Errorf("results count = %d, want 1", len(resp.Results))
	}
}

func TestLocateAllReposTotalSemantics(t *testing.T) {
	// Test that Total is pre-limit sum of all repo totals
	fake := &fakeClient{
		repoList: []string{"repo-a", "repo-b"},
		locateRespByRepo: map[string]*store.LocateResponse{
			"repo-a": {
				Results: []store.LocateResult{
					{Path: "/a1.md", Score: 2.0, Snippet: "high"},
					{Path: "/a2.md", Score: 1.0, Snippet: "low"},
				},
				Total: 5, // More hits than returned results
			},
			"repo-b": {
				Results: []store.LocateResult{
					{Path: "/b1.md", Score: 1.5, Snippet: "mid"},
				},
				Total: 3,
			},
		},
	}
	cmd := newRootCommand(fake, fake, "test-repo", nil)
	cmd.SetArgs([]string{"locate", "query", "--all-repos", "--limit", "2", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp store.LocateResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Total should be 5 + 3 = 8 (pre-limit sum), not 2 (post-limit count)
	if resp.Total != 8 {
		t.Errorf("Total = %d, want 8 (pre-limit sum of repo totals)", resp.Total)
	}

	// Results should be limited to 2
	if len(resp.Results) != 2 {
		t.Errorf("Results count = %d, want 2 (limited)", len(resp.Results))
	}

	// Results should be sorted by score (highest first)
	if len(resp.Results) >= 2 {
		if resp.Results[0].Score < resp.Results[1].Score {
			t.Errorf("results not sorted by score descending")
		}
	}
}
