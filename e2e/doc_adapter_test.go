//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gxfs/internal/store"
	"gxfs/internal/store/postgres"
)

// TestDocAdapterIntegration verifies the read-only DocAdapter returns correct
// results when queried against backfilled document-centric tables.
//
// Test data: same seed as TestBackfillDocsIntegration (5 files in test-repo,
// 3 in mirror-repo). This test runs BackfillDocs first, then exercises every
// read method of DocAdapter.
func TestDocAdapterIntegration(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-doc-adapter-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:            dsn,
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: postgres.FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
		Repo: "test-repo",
	}

	// Setup: create schema, seed data, backfill.
	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)
	seedBackfillData(t, containerName)

	if _, err := postgres.BackfillDocs(ctx, pool, cfg); err != nil {
		t.Fatalf("BackfillDocs: %v", err)
	}

	// Create DocAdapter.
	da := postgres.NewDocAdapter(pool, cfg)

	// --- Write methods must return ErrReadOnlyMount ---
	t.Run("WriteMethods", func(t *testing.T) {
		testWriteMethodsReturnReadOnly(t, ctx, da)
	})

	// --- Cat ---
	t.Run("Cat", func(t *testing.T) {
		testDocCat(t, ctx, da)
	})

	// --- Stat ---
	t.Run("Stat", func(t *testing.T) {
		testDocStat(t, ctx, da)
	})

	// --- LS ---
	t.Run("LS", func(t *testing.T) {
		testDocLS(t, ctx, da)
	})

	// --- Find ---
	t.Run("Find", func(t *testing.T) {
		testDocFind(t, ctx, da)
	})

	// --- Search ---
	t.Run("Search", func(t *testing.T) {
		testDocSearch(t, ctx, da)
	})

	// --- BatchHashes ---
	t.Run("BatchHashes", func(t *testing.T) {
		testDocBatchHashes(t, ctx, da)
	})

	// --- Grep ---
	t.Run("Grep", func(t *testing.T) {
		testDocGrep(t, ctx, da)
	})

	// --- Tree ---
	t.Run("Tree", func(t *testing.T) {
		testDocTree(t, ctx, da)
	})
}

// testWriteMethodsReturnReadOnly verifies Put, Delete, Edit return ErrReadOnlyMount.
func testWriteMethodsReturnReadOnly(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	_, err := da.Put(ctx, store.PutRequest{Path: "/test.txt", Content: "hello"})
	if err != store.ErrReadOnlyMount {
		t.Fatalf("Put error = %v, want ErrReadOnlyMount", err)
	}

	_, err = da.Delete(ctx, store.DeleteRequest{Path: "/test.txt"})
	if err != store.ErrReadOnlyMount {
		t.Fatalf("Delete error = %v, want ErrReadOnlyMount", err)
	}

	_, err = da.Edit(ctx, store.EditRequest{Path: "/README.md", Old: "Hello", New: "Hi"})
	if err != store.ErrReadOnlyMount {
		t.Fatalf("Edit error = %v, want ErrReadOnlyMount", err)
	}
}

// testDocCat verifies Cat returns correct content and hash.
func testDocCat(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	resp, err := da.Cat(ctx, store.CatRequest{Path: "/README.md"})
	if err != nil {
		t.Fatalf("Cat /README.md: %v", err)
	}
	if resp.Content != "Hello World\n" {
		t.Fatalf("Cat /README.md content = %q, want %q", resp.Content, "Hello World\n")
	}
	if resp.Hash == "" {
		t.Fatalf("Cat /README.md hash is empty, want non-empty")
	}

	// Cat non-existent file.
	_, err = da.Cat(ctx, store.CatRequest{Path: "/nonexistent.txt"})
	if err == nil {
		t.Fatal("Cat nonexistent: expected error, got nil")
	}

	// Cat deep nested file.
	resp, err = da.Cat(ctx, store.CatRequest{Path: "/docs/api/reference.md"})
	if err != nil {
		t.Fatalf("Cat /docs/api/reference.md: %v", err)
	}
	if resp.Content != "API reference\n" {
		t.Fatalf("Cat /docs/api/reference.md content = %q, want %q", resp.Content, "API reference\n")
	}
}

// testDocStat verifies Stat for files and implicit directories.
func testDocStat(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	// Stat a file.
	resp, err := da.Stat(ctx, store.StatRequest{Path: "/src/main.go"})
	if err != nil {
		t.Fatalf("Stat /src/main.go: %v", err)
	}
	if resp.Node.Kind != "file" {
		t.Fatalf("Stat /src/main.go kind = %q, want file", resp.Node.Kind)
	}
	if resp.Node.Name != "main.go" {
		t.Fatalf("Stat /src/main.go name = %q, want main.go", resp.Node.Name)
	}
	if resp.Node.Hash == "" {
		t.Fatalf("Stat /src/main.go hash is empty")
	}

	// Stat an implicit directory.
	resp, err = da.Stat(ctx, store.StatRequest{Path: "/docs"})
	if err != nil {
		t.Fatalf("Stat /docs: %v", err)
	}
	if resp.Node.Kind != "dir" {
		t.Fatalf("Stat /docs kind = %q, want dir", resp.Node.Kind)
	}
	if resp.Node.Name != "docs" {
		t.Fatalf("Stat /docs name = %q, want docs", resp.Node.Name)
	}

	// Stat root directory.
	resp, err = da.Stat(ctx, store.StatRequest{Path: "/"})
	if err != nil {
		t.Fatalf("Stat /: %v", err)
	}
	if resp.Node.Kind != "dir" {
		t.Fatalf("Stat / kind = %q, want dir", resp.Node.Kind)
	}

	// Stat nested implicit dir.
	resp, err = da.Stat(ctx, store.StatRequest{Path: "/docs/api"})
	if err != nil {
		t.Fatalf("Stat /docs/api: %v", err)
	}
	if resp.Node.Kind != "dir" {
		t.Fatalf("Stat /docs/api kind = %q, want dir", resp.Node.Kind)
	}

	// Stat nonexistent.
	_, err = da.Stat(ctx, store.StatRequest{Path: "/nope"})
	if err == nil {
		t.Fatal("Stat /nope: expected error, got nil")
	}
}

// testDocLS verifies LS with implicit directories, pagination, and recursive.
func testDocLS(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	// LS / — should have 3 top-level entries: /docs (dir), /src (dir), /README.md (file).
	resp, err := da.LS(ctx, store.LSRequest{Path: "/"})
	if err != nil {
		t.Fatalf("LS /: %v", err)
	}
	if resp.Total != 3 {
		t.Fatalf("LS / total = %d, want 3", resp.Total)
	}
	kinds := make(map[string]string)
	for _, n := range resp.Nodes {
		kinds[n.Name] = n.Kind
	}
	if kinds["docs"] != "dir" {
		t.Fatalf("LS /: 'docs' kind = %q, want dir", kinds["docs"])
	}
	if kinds["src"] != "dir" {
		t.Fatalf("LS /: 'src' kind = %q, want dir", kinds["src"])
	}
	if kinds["README.md"] != "file" {
		t.Fatalf("LS /: 'README.md' kind = %q, want file", kinds["README.md"])
	}

	// LS /docs — should have readme.md (file) + api (dir).
	resp, err = da.LS(ctx, store.LSRequest{Path: "/docs"})
	if err != nil {
		t.Fatalf("LS /docs: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("LS /docs total = %d, want 2", resp.Total)
	}

	// LS /docs/api — should have 2 files.
	resp, err = da.LS(ctx, store.LSRequest{Path: "/docs/api"})
	if err != nil {
		t.Fatalf("LS /docs/api: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("LS /docs/api total = %d, want 2", resp.Total)
	}
	for _, n := range resp.Nodes {
		if n.Kind != "file" {
			t.Fatalf("LS /docs/api: found non-file %q kind=%q", n.Name, n.Kind)
		}
	}

	// LS /docs recursive — should include all nested files.
	resp, err = da.LS(ctx, store.LSRequest{Path: "/docs", Recursive: true})
	if err != nil {
		t.Fatalf("LS /docs recursive: %v", err)
	}
	// /docs/readme.md, /docs/api (dir), /docs/api/reference.md, /docs/api/guide.md
	if resp.Total != 4 {
		t.Fatalf("LS /docs recursive total = %d, want 4", resp.Total)
	}

	// LS pagination: limit=1, offset=0.
	resp, err = da.LS(ctx, store.LSRequest{Path: "/", Limit: 1, Offset: 0})
	if err != nil {
		t.Fatalf("LS / limit=1: %v", err)
	}
	if len(resp.Nodes) != 1 {
		t.Fatalf("LS / limit=1 returned %d nodes, want 1", len(resp.Nodes))
	}
	if resp.Total != 3 {
		t.Fatalf("LS / limit=1 total = %d, want 3", resp.Total)
	}
}

// testDocFind verifies Find with name/depth filters.
func testDocFind(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	// Find all files (no filters).
	resp, err := da.Find(ctx, store.FindRequest{Path: "/"})
	if err != nil {
		t.Fatalf("Find /: %v", err)
	}
	if resp.Total != 5 {
		t.Fatalf("Find / total = %d, want 5", resp.Total)
	}

	// Find by name.
	resp, err = da.Find(ctx, store.FindRequest{Path: "/", Name: "readme.md"})
	if err != nil {
		t.Fatalf("Find name=readme.md: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("Find name=readme.md total = %d, want 1", resp.Total)
	}
	if len(resp.Nodes) > 0 && resp.Nodes[0].Path != "/docs/readme.md" {
		t.Fatalf("Find name=readme.md path = %q, want /docs/readme.md", resp.Nodes[0].Path)
	}

	// Find by case-insensitive name.
	resp, err = da.Find(ctx, store.FindRequest{Path: "/", IName: "README.MD"})
	if err != nil {
		t.Fatalf("Find iname=README.MD: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("Find iname=README.MD total = %d, want 2 (README.md + readme.md)", resp.Total)
	}

	// Find type=dir — DocAdapter only has files, should return 0.
	resp, err = da.Find(ctx, store.FindRequest{Path: "/", Type: "dir"})
	if err != nil {
		t.Fatalf("Find type=dir: %v", err)
	}
	if resp.Total != 0 {
		t.Fatalf("Find type=dir total = %d, want 0 (DocAdapter has no dir rows)", resp.Total)
	}

	// Find with maxdepth=0 (only immediate children, but Find only returns files so depth=0 matches files directly under root).
	resp, err = da.Find(ctx, store.FindRequest{Path: "/", MaxDepth: 0})
	if err != nil {
		t.Fatalf("Find maxdepth=0: %v", err)
	}
	// Only /README.md is directly under / (depth 0).
	if resp.Total != 1 {
		t.Fatalf("Find maxdepth=0 total = %d, want 1", resp.Total)
	}
}

// testDocSearch verifies full-text search over doc tables.
func testDocSearch(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	// Search for "main" — should hit main.go.
	resp, err := da.Search(ctx, store.SearchRequest{Query: "main"})
	if err != nil {
		t.Fatalf("Search 'main': %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("Search 'main' total = %d, want 1", resp.Total)
	}
	if len(resp.Results) > 0 && resp.Results[0].Path != "/src/main.go" {
		t.Fatalf("Search 'main' path = %q, want /src/main.go", resp.Results[0].Path)
	}

	// Search for "hello" — should hit README.md and guide.md.
	resp, err = da.Search(ctx, store.SearchRequest{Query: "hello"})
	if err != nil {
		t.Fatalf("Search 'hello': %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("Search 'hello' total = %d, want 2", resp.Total)
	}

	// Search with path filter.
	resp, err = da.Search(ctx, store.SearchRequest{Query: "hello", Path: "/docs"})
	if err != nil {
		t.Fatalf("Search 'hello' path=/docs: %v", err)
	}
	// Only /docs/api/guide.md is under /docs.
	if resp.Total != 1 {
		t.Fatalf("Search 'hello' path=/docs total = %d, want 1", resp.Total)
	}

	// Search for nonexistent.
	resp, err = da.Search(ctx, store.SearchRequest{Query: "xyzzyplugh"})
	if err != nil {
		t.Fatalf("Search nonexistent: %v", err)
	}
	if resp.Total != 0 {
		t.Fatalf("Search nonexistent total = %d, want 0", resp.Total)
	}
}

// testDocBatchHashes verifies BatchHashes returns hashes for all files.
func testDocBatchHashes(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	resp, err := da.BatchHashes(ctx, store.HashRequest{})
	if err != nil {
		t.Fatalf("BatchHashes: %v", err)
	}
	// test-repo has 5 files, all should have hash.
	if len(resp.Hashes) != 5 {
		t.Fatalf("BatchHashes count = %d, want 5", len(resp.Hashes))
	}
	for _, h := range resp.Hashes {
		if h.Hash == "" {
			t.Fatalf("BatchHashes: empty hash for %q", h.Path)
		}
	}

	// BatchHashes with path filter.
	resp, err = da.BatchHashes(ctx, store.HashRequest{Path: "/docs"})
	if err != nil {
		t.Fatalf("BatchHashes path=/docs: %v", err)
	}
	// /docs/readme.md, /docs/api/reference.md, /docs/api/guide.md
	if len(resp.Hashes) != 3 {
		t.Fatalf("BatchHashes path=/docs count = %d, want 3", len(resp.Hashes))
	}
}

// testDocGrep verifies Grep over doc table content.
func testDocGrep(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	// Grep for "Hello" in all files.
	resp, err := da.Grep(ctx, store.GrepRequest{Path: "/", Pattern: "Hello"})
	if err != nil {
		t.Fatalf("Grep Hello: %v", err)
	}
	// "Hello World\n" appears in README.md and guide.md.
	if len(resp.Matches) != 2 {
		t.Fatalf("Grep Hello matches = %d, want 2", len(resp.Matches))
	}

	// Grep with context.
	resp, err = da.Grep(ctx, store.GrepRequest{Path: "/", Pattern: "func", ContextAfter: 1})
	if err != nil {
		t.Fatalf("Grep func: %v", err)
	}
	if len(resp.Matches) != 1 {
		t.Fatalf("Grep func matches = %d, want 1", len(resp.Matches))
	}
	if resp.Matches[0].Path != "/src/main.go" {
		t.Fatalf("Grep func path = %q, want /src/main.go", resp.Matches[0].Path)
	}
	if len(resp.Matches[0].After) != 1 {
		t.Fatalf("Grep func context after = %d lines, want 1", len(resp.Matches[0].After))
	}

	// Grep with regex.
	resp, err = da.Grep(ctx, store.GrepRequest{Path: "/", Pattern: "^package", Regex: true})
	if err != nil {
		t.Fatalf("Grep regex ^package: %v", err)
	}
	if len(resp.Matches) != 1 {
		t.Fatalf("Grep ^package matches = %d, want 1", len(resp.Matches))
	}

	// Grep with case-insensitive.
	resp, err = da.Grep(ctx, store.GrepRequest{Path: "/", Pattern: "hello", CaseInsensitive: true})
	if err != nil {
		t.Fatalf("Grep hello -i: %v", err)
	}
	if len(resp.Matches) != 2 {
		t.Fatalf("Grep hello -i matches = %d, want 2", len(resp.Matches))
	}

	// Grep with invert match.
	resp, err = da.Grep(ctx, store.GrepRequest{Path: "/src", Pattern: "func", Invert: true})
	if err != nil {
		t.Fatalf("Grep invert: %v", err)
	}
	// main.go has 2 lines: "package main" and "func main() {}"
	// Invert match of "func" should match "package main" only.
	if len(resp.Matches) != 1 {
		t.Fatalf("Grep invert matches = %d, want 1", len(resp.Matches))
	}

	// Grep in specific directory.
	resp, err = da.Grep(ctx, store.GrepRequest{Path: "/docs/api", Pattern: "API"})
	if err != nil {
		t.Fatalf("Grep /docs/api API: %v", err)
	}
	if len(resp.Matches) != 1 {
		t.Fatalf("Grep /docs/api API matches = %d, want 1", len(resp.Matches))
	}

	// Grep with nonexistent path should error.
	_, err = da.Grep(ctx, store.GrepRequest{Path: "/nonexistent", Pattern: "test"})
	if err == nil {
		t.Fatal("Grep /nonexistent: expected error, got nil")
	}

	// Grep with include glob.
	resp, err = da.Grep(ctx, store.GrepRequest{Path: "/", Pattern: "Hello", Include: "*.md"})
	if err != nil {
		t.Fatalf("Grep include *.md: %v", err)
	}
	if len(resp.Matches) != 2 {
		t.Fatalf("Grep include *.md matches = %d, want 2", len(resp.Matches))
	}

	// Grep with exclude glob.
	resp, err = da.Grep(ctx, store.GrepRequest{Path: "/", Pattern: "Hello", Exclude: "*.go"})
	if err != nil {
		t.Fatalf("Grep exclude *.go: %v", err)
	}
	if len(resp.Matches) != 2 {
		t.Fatalf("Grep exclude *.go matches = %d, want 2", len(resp.Matches))
	}

	// Verify match line numbers start at 1.
	for _, m := range resp.Matches {
		if m.Line < 1 {
			t.Fatalf("Grep match line = %d, want >= 1", m.Line)
		}
		if m.Text == "" {
			t.Fatalf("Grep match text is empty for %q line %d", m.Path, m.Line)
		}
	}
}

// testDocTree verifies Tree output with implicit directories.
func testDocTree(t *testing.T, ctx context.Context, da *postgres.DocAdapter) {
	t.Helper()

	// Tree from root.
	resp, err := da.Tree(ctx, store.TreeRequest{Path: "/"})
	if err != nil {
		t.Fatalf("Tree /: %v", err)
	}
	if resp.Root.Kind != "dir" {
		t.Fatalf("Tree / root kind = %q, want dir", resp.Root.Kind)
	}

	// The text should contain all file and directory entries.
	text := resp.Text
	for _, expected := range []string{"docs/", "src/", "README.md", "readme.md", "reference.md", "guide.md", "main.go"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Tree / text missing %q\nfull text:\n%s", expected, text)
		}
	}

	// Tree /docs — should contain api/ and readme.md.
	resp, err = da.Tree(ctx, store.TreeRequest{Path: "/docs"})
	if err != nil {
		t.Fatalf("Tree /docs: %v", err)
	}
	if !strings.Contains(resp.Text, "api/") {
		t.Fatalf("Tree /docs missing api/\ntext:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, "readme.md") {
		t.Fatalf("Tree /docs missing readme.md\ntext:\n%s", resp.Text)
	}

	// Tree with showSize.
	resp, err = da.Tree(ctx, store.TreeRequest{Path: "/", ShowSize: true})
	if err != nil {
		t.Fatalf("Tree / showSize: %v", err)
	}
	// File entries should have [size] suffix.
	if !strings.Contains(resp.Text, "README.md") {
		t.Fatalf("Tree / showSize missing README.md\ntext:\n%s", resp.Text)
	}

	// Tree with dirsOnly.
	resp, err = da.Tree(ctx, store.TreeRequest{Path: "/", DirsOnly: true})
	if err != nil {
		t.Fatalf("Tree / dirsOnly: %v", err)
	}
	// Should contain dirs but not files.
	if !strings.Contains(resp.Text, "docs/") {
		t.Fatalf("Tree / dirsOnly missing docs/\ntext:\n%s", resp.Text)
	}
	if strings.Contains(resp.Text, "README.md") {
		t.Fatalf("Tree / dirsOnly should not contain README.md\ntext:\n%s", resp.Text)
	}

	// Tree nonexistent path.
	_, err = da.Tree(ctx, store.TreeRequest{Path: "/nonexistent"})
	if err == nil {
		t.Fatal("Tree /nonexistent: expected error, got nil")
	}
}

// testDocAdapterMirrorRepo verifies DocAdapter works for a different repo
// using the Repo override on individual requests.
func TestDocAdapterMirrorRepo(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-doc-adapter-mirror-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:            dsn,
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: postgres.FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
		// Default repo is test-repo, but we'll override via request.
		Repo: "test-repo",
	}

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)
	seedBackfillData(t, containerName)

	if _, err := postgres.BackfillDocs(ctx, pool, cfg); err != nil {
		t.Fatalf("BackfillDocs: %v", err)
	}

	da := postgres.NewDocAdapter(pool, cfg)

	// LS mirror-repo via repo override.
	resp, err := da.LS(ctx, store.LSRequest{Repo: "mirror-repo", Path: "/"})
	if err != nil {
		t.Fatalf("LS mirror-repo /: %v", err)
	}
	// mirror-repo has: /docs (dir), /README.md, /docs/readme.md, /docs/api/guide.md
	// Top-level LS: /docs (dir), /README.md (file) = 2 entries.
	if resp.Total != 2 {
		t.Fatalf("LS mirror-repo / total = %d, want 2", resp.Total)
	}

	// Cat mirror-repo file.
	catResp, err := da.Cat(ctx, store.CatRequest{Repo: "mirror-repo", Path: "/docs/api/guide.md"})
	if err != nil {
		t.Fatalf("Cat mirror-repo /docs/api/guide.md: %v", err)
	}
	if catResp.Content != "Hello World\n" {
		t.Fatalf("Cat mirror-repo guide.md = %q, want %q", catResp.Content, "Hello World\n")
	}

	// BatchHashes mirror-repo.
	hashResp, err := da.BatchHashes(ctx, store.HashRequest{Repo: "mirror-repo"})
	if err != nil {
		t.Fatalf("BatchHashes mirror-repo: %v", err)
	}
	if len(hashResp.Hashes) != 3 {
		t.Fatalf("BatchHashes mirror-repo count = %d, want 3", len(hashResp.Hashes))
	}
}
