package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gxfs/internal/store"
)

type fakeClient struct {
	grepReq store.GrepRequest
}

func (f *fakeClient) LS(context.Context, store.LSRequest) (*store.LSResponse, error) {
	return &store.LSResponse{Nodes: []store.Node{
		{Path: "/docs", Name: "docs", Kind: "dir"},
		{Path: "/readme.md", Name: "readme.md", Kind: "file"},
	}}, nil
}

func (f *fakeClient) Tree(context.Context, store.TreeRequest) (*store.TreeResponse, error) {
	return &store.TreeResponse{Text: "/\n  docs/\n"}, nil
}

func (f *fakeClient) Cat(_ context.Context, req store.CatRequest) (*store.CatResponse, error) {
	return &store.CatResponse{Path: req.Path, Content: "# Readme\n"}, nil
}

func (f *fakeClient) Grep(_ context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	f.grepReq = req
	return &store.GrepResponse{Matches: []store.Match{
		{Path: "/go/store.go", Line: 12, Text: "type Adapter interface {"},
	}}, nil
}

func (f *fakeClient) Find(context.Context, store.FindRequest) (*store.FindResponse, error) {
	return &store.FindResponse{Nodes: []store.Node{{Path: "/go/store.go", Name: "store.go", Kind: "file"}}}, nil
}

func (f *fakeClient) Stat(context.Context, store.StatRequest) (*store.StatResponse, error) {
	return &store.StatResponse{Node: store.Node{Path: "/docs", Name: "docs", Kind: "dir"}}, nil
}

func execute(t *testing.T, args ...string) (string, *fakeClient) {
	t.Helper()

	client := &fakeClient{}
	cmd := newRootCommand(client, "gxfs")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}
	return out.String(), client
}

func TestLSOutputMarksDirectories(t *testing.T) {
	got, _ := execute(t, "ls", "/")
	want := "docs/\nreadme.md\n"
	if got != want {
		t.Fatalf("ls output = %q, want %q", got, want)
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
