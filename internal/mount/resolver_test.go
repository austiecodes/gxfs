package mount

import (
	"errors"
	"testing"

	"gxfs/internal/config"
	"gxfs/internal/store"
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

func TestResolverRejectsUnsupportedRemoteForPhaseOne(t *testing.T) {
	_, err := NewResolver("gxfs", []config.MountConfig{
		{Local: "docs/shared", Remote: "collection://openai-go/v3/gotchas", Mode: "readonly"},
	})
	if err == nil {
		t.Fatal("NewResolver() error = nil, want unsupported collection remote")
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
