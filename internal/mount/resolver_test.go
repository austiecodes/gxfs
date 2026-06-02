package mount

import (
	"errors"
	"strings"
	"testing"

	"github.com/austiecodes/gxfs/internal/config"
	"github.com/austiecodes/gxfs/internal/store"
)

func TestResolverUsesLongestLocalPrefix(t *testing.T) {
	r, err := NewResolver("github.com/acme/project", []config.MountConfig{
		{Local: "docs", Remote: "repo://self/docs", Mode: "writable"},
		{Local: "docs/gotchas/openai-go", Remote: "repo://self/shared/openai-go", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	resolved, err := r.Resolve("docs/gotchas/openai-go/f.md", OpRead)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.RemoteRepo != "github.com/acme/project" {
		t.Fatalf("resolved.RemoteRepo = %q, want github.com/acme/project", resolved.RemoteRepo)
	}
	if resolved.Source.Kind != store.SourceKindRepo || resolved.Source.Name != "github.com/acme/project" {
		t.Fatalf("resolved.Source = %+v, want repo source for github.com/acme/project", resolved.Source)
	}
	if resolved.RemotePath != "/shared/openai-go/f.md" {
		t.Fatalf("resolved.RemotePath = %q, want /shared/openai-go/f.md", resolved.RemotePath)
	}
	if resolved.LocalPath != "docs/gotchas/openai-go/f.md" {
		t.Fatalf("resolved.LocalPath = %q, want docs/gotchas/openai-go/f.md", resolved.LocalPath)
	}
}

func TestResolverRejectsWriteToReadOnlyMount(t *testing.T) {
	r, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/shared", Remote: "repo://self/shared", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	_, err = r.Resolve("docs/shared/a.md", OpWrite)
	if !errors.Is(err, store.ErrReadOnlyMount) {
		t.Fatalf("Resolve() error = %v, want ErrReadOnlyMount", err)
	}
}

func TestResolverMapsRemotePathBackToLocal(t *testing.T) {
	r, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs", Remote: "repo://self/remote-docs", Mode: "writable"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	local, ok := r.ToLocal("gxfs", "/remote-docs/guide.md")
	if !ok {
		t.Fatal("ToLocal() ok = false, want true")
	}
	if local != "docs/guide.md" {
		t.Fatalf("local = %q, want docs/guide.md", local)
	}
}

func TestResolverAcceptsDocsetRemote(t *testing.T) {
	r, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/shared", Remote: "docset://openai-go/v3/gotchas", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	resolved, err := r.Resolve("docs/shared/guide.md", OpRead)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Source.Kind != store.SourceKindDocset || resolved.Source.Name != "openai-go" || resolved.Source.Path != "/v3/gotchas/guide.md" {
		t.Fatalf("resolved.Source = %+v, want docset://openai-go/v3/gotchas/guide.md", resolved.Source)
	}
}

func TestSourceRefParseAndFormat(t *testing.T) {
	tests := []struct {
		raw        string
		wantKind   store.SourceKind
		wantName   string
		wantPath   string
		wantString string
	}{
		{"repo://github%2Fopenai-go/docs", store.SourceKindRepo, "github/openai-go", "/docs", "repo://github%2Fopenai-go/docs"},
		{"docs://openai-go-sdk", store.SourceKindDocs, "openai-go-sdk", "/", "docs://openai-go-sdk"},
		{"docs://openai-go-sdk/usage.md", store.SourceKindDocs, "openai-go-sdk", "/usage.md", "docs://openai-go-sdk/usage.md"},
		{"docs://github%2Fopenai-go/usage.md", store.SourceKindDocs, "github/openai-go", "/usage.md", "docs://github%2Fopenai-go/usage.md"},
		{"docset://best-practices", store.SourceKindDocset, "best-practices", "/", "docset://best-practices"},
	}

	for _, tt := range tests {
		ref, err := store.ParseSourceRef(tt.raw)
		if err != nil {
			t.Fatalf("ParseSourceRef(%q) error = %v", tt.raw, err)
		}
		if ref.Kind != tt.wantKind || ref.Name != tt.wantName || ref.Path != tt.wantPath {
			t.Fatalf("ParseSourceRef(%q) = %+v, want kind=%s name=%q path=%q", tt.raw, ref, tt.wantKind, tt.wantName, tt.wantPath)
		}
		if ref.String() != tt.wantString {
			t.Fatalf("ParseSourceRef(%q).String() = %q, want %q", tt.raw, ref.String(), tt.wantString)
		}
	}
}

func TestResolverSupportsDocsSourceRefs(t *testing.T) {
	r, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/openai-go-sdk", Remote: "docs://openai-go-sdk", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	resolved, err := r.Resolve("docs/openai-go-sdk/usage.md", OpRead)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Source.Kind != store.SourceKindDocs || resolved.Source.Name != "openai-go-sdk" || resolved.Source.Path != "/usage.md" {
		t.Fatalf("resolved.Source = %+v, want docs://openai-go-sdk/usage.md", resolved.Source)
	}
	if resolved.RemoteRepo != "" || resolved.RemotePath != "/usage.md" {
		t.Fatalf("compat fields = repo %q path %q, want empty repo and /usage.md", resolved.RemoteRepo, resolved.RemotePath)
	}

	local, ok := r.ToLocalSource(store.SourceRef{Kind: store.SourceKindDocs, Name: "openai-go-sdk", Path: "/usage.md"})
	if !ok {
		t.Fatal("ToLocalSource() ok = false, want true")
	}
	if local != "docs/openai-go-sdk/usage.md" {
		t.Fatalf("local = %q, want docs/openai-go-sdk/usage.md", local)
	}
}

func TestResolverRejectsRepoSelfWithoutPath(t *testing.T) {
	_, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs", Remote: "repo://self", Mode: "writable"},
	})
	if err == nil {
		t.Fatal("NewResolver() error = nil, want error for repo://self without path")
	}
	if !strings.Contains(err.Error(), "needs a path after self/") {
		t.Fatalf("error = %q, want hint about self/ path", err.Error())
	}
}

func TestResolverCrossRepoMount(t *testing.T) {
	r, err := NewResolver("my-project", []config.MountConfig{
		{Local: "docs", Remote: "repo://self/docs", Mode: "writable"},
		{Local: "vendor/openai-go", Remote: "repo://github%2Fopenai-go/", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	// Resolve cross-repo mount point
	resolved, err := r.Resolve("vendor/openai-go/docs/quickstart.md", OpRead)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.RemoteRepo != "github/openai-go" {
		t.Fatalf("resolved.RemoteRepo = %q, want github/openai-go", resolved.RemoteRepo)
	}
	if resolved.RemotePath != "/docs/quickstart.md" {
		t.Fatalf("resolved.RemotePath = %q, want /docs/quickstart.md", resolved.RemotePath)
	}
	if resolved.Mode != "readonly" {
		t.Fatalf("resolved.Mode = %q, want readonly", resolved.Mode)
	}

	// Cross-repo mount is readonly — writes should fail
	_, err = r.Resolve("vendor/openai-go/docs/quickstart.md", OpWrite)
	if !errors.Is(err, store.ErrReadOnlyMount) {
		t.Fatalf("Resolve() error = %v, want ErrReadOnlyMount", err)
	}
}

func TestResolverCrossRepoSubtreeMount(t *testing.T) {
	r, err := NewResolver("my-project", []config.MountConfig{
		{Local: "vendor/openai-docs", Remote: "repo://github%2Fopenai-go/docs", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	resolved, err := r.Resolve("vendor/openai-docs/api/chat.md", OpRead)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.RemoteRepo != "github/openai-go" {
		t.Fatalf("resolved.RemoteRepo = %q, want github/openai-go", resolved.RemoteRepo)
	}
	if resolved.RemotePath != "/docs/api/chat.md" {
		t.Fatalf("resolved.RemotePath = %q, want /docs/api/chat.md", resolved.RemotePath)
	}
}

func TestResolverRejectsSelfRootMount(t *testing.T) {
	_, err := NewResolver("my-project", []config.MountConfig{
		{Local: "docs", Remote: "repo://self/", Mode: "writable"},
	})
	if err == nil {
		t.Fatal("NewResolver() error = nil, want error for self root mount")
	}
	if !strings.Contains(err.Error(), "self root mount") {
		t.Fatalf("error = %q, want hint about self root mount", err.Error())
	}
}

func TestResolverRejectsCrossRepoWithoutRepoName(t *testing.T) {
	_, err := NewResolver("my-project", []config.MountConfig{
		{Local: "docs", Remote: "repo:///", Mode: "readonly"},
	})
	if err == nil {
		t.Fatal("NewResolver() error = nil, want error for missing repo name")
	}
}

func TestResolverCrossRepoToLocal(t *testing.T) {
	r, err := NewResolver("my-project", []config.MountConfig{
		{Local: "vendor/openai-go", Remote: "repo://github%2Fopenai-go/", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	local, ok := r.ToLocal("github/openai-go", "/docs/quickstart.md")
	if !ok {
		t.Fatal("ToLocal() ok = false, want true")
	}
	if local != "vendor/openai-go/docs/quickstart.md" {
		t.Fatalf("local = %q, want vendor/openai-go/docs/quickstart.md", local)
	}

	// Self repo should not match cross-repo mount
	_, ok = r.ToLocal("my-project", "/docs/quickstart.md")
	if ok {
		t.Fatal("ToLocal() ok = true for my-project, want false")
	}
}

func TestParseRemoteRef(t *testing.T) {
	tests := []struct {
		raw      string
		wantRepo string
		wantPath string
		wantErr  bool
	}{
		{"repo://self/docs", "my-project", "/docs", false},
		{"repo://self/", "", "", true}, // self root rejected
		{"repo://other-repo/", "other-repo", "/", false},
		{"repo://github%2Fopenai-go/", "github/openai-go", "/", false},
		{"repo://github%2Fopenai-go/docs", "github/openai-go", "/docs", false},
		{"repo://other-repo/docs", "other-repo", "/docs", false},
		{"repo://other-repo", "other-repo", "/", false},
		{"docset://stuff", "", "", true},
		{"unknown://thing", "", "", true},
	}

	for _, tt := range tests {
		repo, path, err := ParseRemoteRef("my-project", tt.raw)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseRemoteRef(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if repo != tt.wantRepo {
			t.Errorf("ParseRemoteRef(%q) repo = %q, want %q", tt.raw, repo, tt.wantRepo)
		}
		if path != tt.wantPath {
			t.Errorf("ParseRemoteRef(%q) path = %q, want %q", tt.raw, path, tt.wantPath)
		}
	}
}

func TestParseRemoteRefRoundTripWithSlashInRepo(t *testing.T) {
	// A repo name containing "/" must round-trip through URL-encoded refs.
	repoName := "github/openai-go"
	ref := "repo://github%2Fopenai-go/docs/quickstart.md"

	// Parse the ref back
	parsedRepo, parsedPath, err := ParseRemoteRef("my-project", ref)
	if err != nil {
		t.Fatalf("ParseRemoteRef() error = %v", err)
	}
	if parsedRepo != repoName {
		t.Fatalf("parsedRepo = %q, want %q", parsedRepo, repoName)
	}
	if parsedPath != "/docs/quickstart.md" {
		t.Fatalf("parsedPath = %q, want /docs/quickstart.md", parsedPath)
	}
}

func TestResolverDetectsVirtualDirsFromMountPaths(t *testing.T) {
	r, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/gotchas/openai-go", Remote: "repo://self/shared/openai-go", Mode: "readonly"},
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	if !r.hasVirtualDir("/") {
		t.Fatal("hasVirtualDir(/) = false, want true")
	}
	if !r.hasVirtualDir("docs") {
		t.Fatal("hasVirtualDir(docs) = false, want true")
	}
	if !r.hasVirtualDir("docs/gotchas") {
		t.Fatal("hasVirtualDir(docs/gotchas) = false, want true")
	}
	if r.hasVirtualDir("src") {
		t.Fatal("hasVirtualDir(src) = true, want false")
	}
}
