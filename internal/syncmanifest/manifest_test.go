package syncmanifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanLocalDirComputesHashAndRelativePaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "a.md"), "alpha")
	writeFile(t, filepath.Join(dir, "docs", "nested", "b.md"), "bravo")
	t.Chdir(dir)

	files, err := ScanLocal("docs")
	if err != nil {
		t.Fatalf("ScanLocal() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ScanLocal() len = %d, want 2: %+v", len(files), files)
	}
	if files[0].LocalPath != "docs/a.md" || files[1].LocalPath != "docs/nested/b.md" {
		t.Fatalf("LocalPath = %q/%q, want docs/a.md/docs/nested/b.md", files[0].LocalPath, files[1].LocalPath)
	}
	if files[0].ContentHash != "sha256:8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8" {
		t.Fatalf("ContentHash = %q, want alpha hash", files[0].ContentHash)
	}
}

func TestManifestSaveLoadAndUpsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gxfs", "manifest.toml")
	manifest := Upsert(Manifest{}, []Entry{
		{Local: "docs/b.md", RemoteDoc: "repo://self/docs/b.md", ContentHash: "sha256:b"},
		{Local: "docs/a.md", RemoteDoc: "repo://self/docs/a.md", ContentHash: "sha256:a"},
	})

	if err := Save(path, manifest); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("Version = %d, want 1", got.Version)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries len = %d, want 2", len(got.Entries))
	}
	if got.Entries[0].Local != "docs/a.md" || got.Entries[1].Local != "docs/b.md" {
		t.Fatalf("Entries not sorted by local path: %+v", got.Entries)
	}

	got = Upsert(got, []Entry{{Local: "docs/a.md", ContentHash: "sha256:new"}})
	if len(got.Entries) != 2 {
		t.Fatalf("Entries len after upsert = %d, want 2", len(got.Entries))
	}
	if got.Entries[0].ContentHash != "sha256:new" {
		t.Fatalf("upserted hash = %q, want sha256:new", got.Entries[0].ContentHash)
	}
}

func TestReplaceUnderRefreshesOnlyRequestedRoot(t *testing.T) {
	manifest := Manifest{Entries: []Entry{
		{Local: "docs/old.md", ContentHash: "sha256:old"},
		{Local: "docs/nested/old.md", ContentHash: "sha256:nested"},
		{Local: "other/keep.md", ContentHash: "sha256:keep"},
	}}

	got := ReplaceUnder(manifest, "docs", []Entry{{Local: "docs/new.md", ContentHash: "sha256:new"}})
	if len(got.Entries) != 2 {
		t.Fatalf("Entries len = %d, want 2: %+v", len(got.Entries), got.Entries)
	}
	if got.Entries[0].Local != "docs/new.md" || got.Entries[1].Local != "other/keep.md" {
		t.Fatalf("Entries after replace = %+v, want docs/new.md and other/keep.md", got.Entries)
	}
}

func TestReplaceUnderPreservesNestedSubMountEntries(t *testing.T) {
	// Regression test: sync pull on a parent mount should not clobber
	// nested cross-repo sub-mount entries that live under the same path prefix.
	manifest := Manifest{Entries: []Entry{
		{Local: "docs/readme.md", Mount: "docs", ContentHash: "sha256:own"},
		{Local: "docs/lib/openai-go/quickstart.md", Mount: "docs/lib/openai-go", ContentHash: "sha256:cross"},
		{Local: "other/keep.md", Mount: "other", ContentHash: "sha256:keep"},
	}}

	// Pull on parent mount "docs" — should replace docs/readme.md but
	// preserve docs/lib/openai-go/quickstart.md because its mount is different.
	got := ReplaceUnder(manifest, "docs", []Entry{
		{Local: "docs/readme.md", Mount: "docs", ContentHash: "sha256:new"},
	})

	if len(got.Entries) != 3 {
		t.Fatalf("Entries len = %d, want 3: %+v", len(got.Entries), got.Entries)
	}

	// Check that the sub-mount entry survived
	found := false
	for _, e := range got.Entries {
		if e.Local == "docs/lib/openai-go/quickstart.md" {
			found = true
			if e.ContentHash != "sha256:cross" {
				t.Fatalf("sub-mount entry hash = %q, want sha256:cross", e.ContentHash)
			}
		}
	}
	if !found {
		t.Fatal("sub-mount entry docs/lib/openai-go/quickstart.md was clobbered")
	}

	// Check that the parent entry was updated
	for _, e := range got.Entries {
		if e.Local == "docs/readme.md" && e.ContentHash != "sha256:new" {
			t.Fatalf("parent entry hash = %q, want sha256:new", e.ContentHash)
		}
	}
}

func TestEntriesUnderAndUpdateEntries(t *testing.T) {
	manifest := Manifest{Entries: []Entry{
		{Local: "docs/a.md", ContentHash: "sha256:a", Materialized: true},
		{Local: "docs/nested/b.md", ContentHash: "sha256:b", Materialized: true},
		{Local: "other/c.md", ContentHash: "sha256:c", Materialized: true},
	}}

	entries := EntriesUnder(manifest, "docs")
	if len(entries) != 2 {
		t.Fatalf("EntriesUnder len = %d, want 2: %+v", len(entries), entries)
	}
	entries[0].Materialized = false
	got := UpdateEntries(manifest, entries[:1])
	if got.Entries[0].Local != "docs/a.md" || got.Entries[0].Materialized {
		t.Fatalf("updated entry = %+v, want docs/a.md dematerialized", got.Entries[0])
	}
	if !got.Entries[1].Materialized || !got.Entries[2].Materialized {
		t.Fatalf("unupdated entries changed: %+v", got.Entries)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
