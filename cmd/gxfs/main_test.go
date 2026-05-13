package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gxfs/internal/vfs"

	"gxfs/internal/config"
	"gxfs/internal/mount"
	"gxfs/internal/store"
	"gxfs/internal/syncmanifest"
)

type fakeClient struct {
	grepReq     store.GrepRequest
	grepMatches []store.Match
	lsNodes     []store.Node
	statNode    *store.Node
	catContent  string
	lsReq       store.LSRequest
	findReq     store.FindRequest
	findNodes   []store.Node
	treeReq     store.TreeRequest
	treeText    string
	catReqs     []store.CatRequest
	catContents map[string]string
	putReqs     []store.PutRequest
	searchReq   store.SearchRequest
	searchResp  *store.SearchResponse
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
	f.catReqs = append(f.catReqs, req)
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

func (f *fakeClient) Stat(context.Context, store.StatRequest) (*store.StatResponse, error) {
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

func (f *fakeClient) Edit(context.Context, store.EditRequest) (*store.EditResponse, error) {
	return nil, nil
}

func (f *fakeClient) BatchHashes(_ context.Context, _ store.HashRequest) (*store.HashResponse, error) {
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
			got := formatLSLine(tt.node, tt.long, tt.classify, tt.slashDir)
			if got != tt.want {
				t.Fatalf("formatLSLine() = %q, want %q", got, tt.want)
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

	cmd := newInitCommand()
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
	if !strings.Contains(agents, gxfsInstructionsStart) || !strings.Contains(agents, gxfsInstructionsEnd) {
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
	if got := strings.Count(agents, gxfsInstructionsStart); got != 1 {
		t.Fatalf("GXFS start marker count = %d, want 1 in %q", got, agents)
	}
	if got := strings.Count(agents, gxfsInstructionsEnd); got != 1 {
		t.Fatalf("GXFS end marker count = %d, want 1 in %q", got, agents)
	}
}

func TestInitAgentClaudeWritesClaudeMD(t *testing.T) {
	dir := t.TempDir()
	executeInit(t, "--agent", "claude", dir)

	claude := readTextFile(t, filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(claude, gxfsInstructionsStart) {
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
	if !strings.Contains(claude, gxfsInstructionsStart) {
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
	if !strings.Contains(claude, gxfsInstructionsStart) {
		t.Fatalf("CLAUDE.md missing GXFS instructions through symlink: %q", claude)
	}
	if !strings.Contains(got, "resolved to") {
		t.Fatalf("init output = %q, want resolved symlink target", got)
	}
}

func TestInitRejectsUnsupportedAgent(t *testing.T) {
	dir := t.TempDir()
	cmd := newInitCommand()
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
	if !strings.Contains(err.Error(), "only repo://self/<path>") {
		t.Fatalf("error = %q, want repo://self rejection", err)
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
