//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"gxfs/internal/store"
	"gxfs/internal/store/postgres"
)

// TestDocAdapterIntegration verifies the read-only DocAdapter returns results
// equivalent to the old Adapter after backfill.
//
// Strategy: run BackfillDocs, then create both old Adapter and DocAdapter,
// and compare their responses for LS/Cat/Stat/Find/Search/BatchHashes/Grep/Tree.
// The old Adapter reads from vfs_nodes/vfs_content, the DocAdapter reads from
// gxfs_docs/gxfs_repo_paths. Results must be equivalent.
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

	// Create both adapters.
	oldAdapter := postgres.New(pool, cfg)

	docAdapter := postgres.NewDocAdapter(pool, cfg)

	// --- Write methods must return ErrReadOnlyMount ---
	t.Run("WriteMethods", func(t *testing.T) {
		testWriteMethodsReturnReadOnly(t, ctx, docAdapter)
	})

	// --- Comparison tests: old vs new ---
	t.Run("Cat", func(t *testing.T) {
		compareCat(t, ctx, oldAdapter, docAdapter)
	})

	t.Run("Stat", func(t *testing.T) {
		compareStat(t, ctx, oldAdapter, docAdapter)
	})

	t.Run("LS", func(t *testing.T) {
		compareLS(t, ctx, oldAdapter, docAdapter)
	})

	t.Run("Find", func(t *testing.T) {
		compareFind(t, ctx, oldAdapter, docAdapter)
	})

	t.Run("Search", func(t *testing.T) {
		compareSearch(t, ctx, oldAdapter, docAdapter)
	})

	t.Run("BatchHashes", func(t *testing.T) {
		compareBatchHashes(t, ctx, oldAdapter, docAdapter)
	})

	t.Run("Grep", func(t *testing.T) {
		compareGrep(t, ctx, oldAdapter, docAdapter)
	})

	t.Run("Tree", func(t *testing.T) {
		compareTree(t, ctx, oldAdapter, docAdapter)
	})

	// --- Edge cases ---
	t.Run("NonexistentRoot", func(t *testing.T) {
		compareNonexistentRoot(t, ctx, oldAdapter, docAdapter)
	})

	t.Run("RelativePath", func(t *testing.T) {
		compareRelativePath(t, ctx, oldAdapter, docAdapter)
	})
}

// testWriteMethodsReturnReadOnly verifies Put, Delete, Edit return ErrReadOnlyMount.
func testWriteMethodsReturnReadOnly(t *testing.T, ctx context.Context, da store.Adapter) {
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

// --- Comparison helpers ---

func compareCat(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	paths := []string{"/README.md", "/docs/readme.md", "/docs/api/reference.md", "/src/main.go"}
	for _, p := range paths {
		oldResp, err := old.Cat(ctx, store.CatRequest{Path: p})
		if err != nil {
			t.Fatalf("old Cat %s: %v", p, err)
		}
		newResp, err := new.Cat(ctx, store.CatRequest{Path: p})
		if err != nil {
			t.Fatalf("new Cat %s: %v", p, err)
		}
		if oldResp.Content != newResp.Content {
			t.Fatalf("Cat %s content mismatch:\nold: %q\nnew: %q", p, oldResp.Content, newResp.Content)
		}
		if newResp.Hash == "" {
			t.Fatalf("Cat %s hash is empty", p)
		}
	}

	// Both should 404 for nonexistent.
	_, oldErr := old.Cat(ctx, store.CatRequest{Path: "/nonexistent.txt"})
	_, newErr := new.Cat(ctx, store.CatRequest{Path: "/nonexistent.txt"})
	if oldErr == nil || newErr == nil {
		t.Fatal("Cat nonexistent: expected errors from both adapters")
	}
}

func compareStat(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	// Stat files.
	paths := []string{"/src/main.go", "/README.md", "/docs/api/guide.md"}
	for _, p := range paths {
		oldResp, err := old.Stat(ctx, store.StatRequest{Path: p})
		if err != nil {
			t.Fatalf("old Stat %s: %v", p, err)
		}
		newResp, err := new.Stat(ctx, store.StatRequest{Path: p})
		if err != nil {
			t.Fatalf("new Stat %s: %v", p, err)
		}
		if oldResp.Node.Kind != newResp.Node.Kind {
			t.Fatalf("Stat %s kind: old=%q new=%q", p, oldResp.Node.Kind, newResp.Node.Kind)
		}
		if oldResp.Node.Name != newResp.Node.Name {
			t.Fatalf("Stat %s name: old=%q new=%q", p, oldResp.Node.Name, newResp.Node.Name)
		}
		if newResp.Node.Hash == "" {
			t.Fatalf("Stat %s hash is empty", p)
		}
	}

	// Stat implicit dirs.
	for _, p := range []string{"/docs", "/docs/api", "/src"} {
		oldResp, err := old.Stat(ctx, store.StatRequest{Path: p})
		if err != nil {
			t.Fatalf("old Stat dir %s: %v", p, err)
		}
		newResp, err := new.Stat(ctx, store.StatRequest{Path: p})
		if err != nil {
			t.Fatalf("new Stat dir %s: %v", p, err)
		}
		if oldResp.Node.Kind != "dir" || newResp.Node.Kind != "dir" {
			t.Fatalf("Stat dir %s: old kind=%q new kind=%q", p, oldResp.Node.Kind, newResp.Node.Kind)
		}
	}

	// Root dir.
	oldResp, err := old.Stat(ctx, store.StatRequest{Path: "/"})
	if err != nil {
		t.Fatalf("old Stat /: %v", err)
	}
	newResp, err := new.Stat(ctx, store.StatRequest{Path: "/"})
	if err != nil {
		t.Fatalf("new Stat /: %v", err)
	}
	if oldResp.Node.Kind != "dir" || newResp.Node.Kind != "dir" {
		t.Fatalf("Stat /: old=%q new=%q", oldResp.Node.Kind, newResp.Node.Kind)
	}

	// Nonexistent.
	_, oldErr := old.Stat(ctx, store.StatRequest{Path: "/nope"})
	_, newErr := new.Stat(ctx, store.StatRequest{Path: "/nope"})
	if oldErr == nil || newErr == nil {
		t.Fatal("Stat /nope: both should error")
	}
}

func compareLS(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	cases := []struct {
		name string
		req  store.LSRequest
	}{
		{"root", store.LSRequest{Path: "/"}},
		{"root recursive", store.LSRequest{Path: "/", Recursive: true}},
		{"docs", store.LSRequest{Path: "/docs"}},
		{"docs recursive", store.LSRequest{Path: "/docs", Recursive: true}},
		{"docs/api", store.LSRequest{Path: "/docs/api"}},
		{"src", store.LSRequest{Path: "/src"}},
		{"root limit1", store.LSRequest{Path: "/", Limit: 1}},
		{"root offset1", store.LSRequest{Path: "/", Offset: 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldResp, err := old.LS(ctx, tc.req)
			if err != nil {
				t.Fatalf("old LS: %v", err)
			}
			newResp, err := new.LS(ctx, tc.req)
			if err != nil {
				t.Fatalf("new LS: %v", err)
			}

			// Compare totals.
			if oldResp.Total != newResp.Total {
				t.Fatalf("total mismatch: old=%d new=%d", oldResp.Total, newResp.Total)
			}

			// Compare node kinds and names (order may differ).
			oldNodes := nodeSet(oldResp.Nodes)
			newNodes := nodeSet(newResp.Nodes)
			for k, v := range oldNodes {
				if newNodes[k] != v {
					t.Fatalf("node mismatch for %q: old kind=%q new kind=%q", k, v, newNodes[k])
				}
			}
			if len(oldNodes) != len(newNodes) {
				t.Fatalf("node count: old=%d new=%d\nold: %v\nnew: %v", len(oldNodes), len(newNodes), oldNodes, newNodes)
			}
		})
	}

	// Nonexistent root: both should error.
	t.Run("nonexistent", func(t *testing.T) {
		_, oldErr := old.LS(ctx, store.LSRequest{Path: "/nope"})
		_, newErr := new.LS(ctx, store.LSRequest{Path: "/nope"})
		if oldErr == nil {
			t.Fatal("old LS /nope: expected error")
		}
		if newErr == nil {
			t.Fatal("new LS /nope: expected error")
		}
	})
}

func compareFind(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	cases := []struct {
		name string
		req  store.FindRequest
	}{
		{"all files", store.FindRequest{Path: "/"}},
		{"name readme.md", store.FindRequest{Path: "/", Name: "readme.md"}},
		{"name glob *.md", store.FindRequest{Path: "/", Name: "*.md"}},
		{"iname README.MD", store.FindRequest{Path: "/", IName: "README.MD"}},
		{"type file", store.FindRequest{Path: "/", Type: "file"}},
		{"type dir", store.FindRequest{Path: "/", Type: "dir"}},
		{"maxdepth 1", store.FindRequest{Path: "/", MaxDepth: 1}},
		{"maxdepth 2", store.FindRequest{Path: "/", MaxDepth: 2}},
		{"mindepth 1", store.FindRequest{Path: "/", MinDepth: 1}},
		{"under /docs", store.FindRequest{Path: "/docs"}},
		{"name *.go under /", store.FindRequest{Path: "/", Name: "*.go"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldResp, err := old.Find(ctx, tc.req)
			if err != nil {
				t.Fatalf("old Find: %v", err)
			}
			newResp, err := new.Find(ctx, tc.req)
			if err != nil {
				t.Fatalf("new Find: %v", err)
			}

			// Compare totals.
			if oldResp.Total != newResp.Total {
				t.Fatalf("total mismatch: old=%d new=%d\nold paths: %v\nnew paths: %v",
					oldResp.Total, newResp.Total,
					nodePaths(oldResp.Nodes), nodePaths(newResp.Nodes))
			}

			// Compare paths (ignore order).
			oldPaths := sortedNodePaths(oldResp.Nodes)
			newPaths := sortedNodePaths(newResp.Nodes)
			for i := range oldPaths {
				if oldPaths[i] != newPaths[i] {
					t.Fatalf("path mismatch at %d: old=%q new=%q", i, oldPaths[i], newPaths[i])
				}
			}
		})
	}

	// Nonexistent root: both should error.
	t.Run("nonexistent", func(t *testing.T) {
		_, oldErr := old.Find(ctx, store.FindRequest{Path: "/nope"})
		_, newErr := new.Find(ctx, store.FindRequest{Path: "/nope"})
		if oldErr == nil {
			t.Fatal("old Find /nope: expected error")
		}
		if newErr == nil {
			t.Fatal("new Find /nope: expected error")
		}
	})
}

func compareSearch(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	cases := []struct {
		name  string
		query string
		path  string
	}{
		{"main", "main", "/"},
		{"hello", "hello", "/"},
		{"hello under /docs", "hello", "/docs"},
		{"nonexistent", "xyzzyplugh", "/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldResp, err := old.Search(ctx, store.SearchRequest{Query: tc.query, Path: tc.path})
			if err != nil {
				t.Fatalf("old Search: %v", err)
			}
			newResp, err := new.Search(ctx, store.SearchRequest{Query: tc.query, Path: tc.path})
			if err != nil {
				t.Fatalf("new Search: %v", err)
			}

			if oldResp.Total != newResp.Total {
				t.Fatalf("total mismatch: old=%d new=%d", oldResp.Total, newResp.Total)
			}

			oldPaths := searchPaths(oldResp.Results)
			newPaths := searchPaths(newResp.Results)
			if len(oldPaths) != len(newPaths) {
				t.Fatalf("result count: old=%d new=%d", len(oldPaths), len(newPaths))
			}
			for i := range oldPaths {
				if oldPaths[i] != newPaths[i] {
					t.Fatalf("path mismatch at %d: old=%q new=%q", i, oldPaths[i], newPaths[i])
				}
			}
		})
	}
}

func compareBatchHashes(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	cases := []struct {
		name string
		path string
	}{
		{"all", "/"},
		{"under /docs", "/docs"},
		{"under /src", "/src"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldResp, err := old.BatchHashes(ctx, store.HashRequest{Path: tc.path})
			if err != nil {
				t.Fatalf("old BatchHashes: %v", err)
			}
			newResp, err := new.BatchHashes(ctx, store.HashRequest{Path: tc.path})
			if err != nil {
				t.Fatalf("new BatchHashes: %v", err)
			}

			if len(oldResp.Hashes) != len(newResp.Hashes) {
				t.Fatalf("hash count: old=%d new=%d", len(oldResp.Hashes), len(newResp.Hashes))
			}

			oldMap := hashMap(oldResp.Hashes)
			newMap := hashMap(newResp.Hashes)
			for p, h := range oldMap {
				if newMap[p] != h {
					t.Fatalf("hash mismatch for %q: old=%q new=%q", p, h, newMap[p])
				}
			}
		})
	}
}

func compareGrep(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	cases := []struct {
		name string
		req  store.GrepRequest
	}{
		{"Hello", store.GrepRequest{Path: "/", Pattern: "Hello"}},
		{"func", store.GrepRequest{Path: "/", Pattern: "func"}},
		{"regex ^package", store.GrepRequest{Path: "/", Pattern: "^package", Regex: true}},
		{"case-insensitive hello", store.GrepRequest{Path: "/", Pattern: "hello", CaseInsensitive: true}},
		{"invert func /src", store.GrepRequest{Path: "/src", Pattern: "func", Invert: true}},
		{"API in /docs/api", store.GrepRequest{Path: "/docs/api", Pattern: "API"}},
		{"include *.md", store.GrepRequest{Path: "/", Pattern: "Hello", Include: "*.md"}},
		{"exclude *.go", store.GrepRequest{Path: "/", Pattern: "Hello", Exclude: "*.go"}},
		{"context after", store.GrepRequest{Path: "/", Pattern: "func", ContextAfter: 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldResp, err := old.Grep(ctx, tc.req)
			if err != nil {
				t.Fatalf("old Grep: %v", err)
			}
			newResp, err := new.Grep(ctx, tc.req)
			if err != nil {
				t.Fatalf("new Grep: %v", err)
			}

			if len(oldResp.Matches) != len(newResp.Matches) {
				t.Fatalf("match count: old=%d new=%d\nold: %v\nnew: %v",
					len(oldResp.Matches), len(newResp.Matches),
					matchPaths(oldResp.Matches), matchPaths(newResp.Matches))
			}

			oldMatches := matchSet(oldResp.Matches)
			newMatches := matchSet(newResp.Matches)
			for k := range oldMatches {
				if !newMatches[k] {
					t.Fatalf("old match %q not found in new results", k)
				}
			}
		})
	}

	// Nonexistent path: both should error.
	t.Run("nonexistent", func(t *testing.T) {
		_, oldErr := old.Grep(ctx, store.GrepRequest{Path: "/nope", Pattern: "test"})
		_, newErr := new.Grep(ctx, store.GrepRequest{Path: "/nope", Pattern: "test"})
		if oldErr == nil {
			t.Fatal("old Grep /nope: expected error")
		}
		if newErr == nil {
			t.Fatal("new Grep /nope: expected error")
		}
	})
}

func compareTree(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	cases := []struct {
		name string
		req  store.TreeRequest
	}{
		{"root", store.TreeRequest{Path: "/"}},
		{"docs", store.TreeRequest{Path: "/docs"}},
		{"showSize", store.TreeRequest{Path: "/", ShowSize: true}},
		{"dirsOnly", store.TreeRequest{Path: "/", DirsOnly: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldResp, err := old.Tree(ctx, tc.req)
			if err != nil {
				t.Fatalf("old Tree: %v", err)
			}
			newResp, err := new.Tree(ctx, tc.req)
			if err != nil {
				t.Fatalf("new Tree: %v", err)
			}

			// Both should have root dir.
			if oldResp.Root.Kind != "dir" || newResp.Root.Kind != "dir" {
				t.Fatalf("root kind: old=%q new=%q", oldResp.Root.Kind, newResp.Root.Kind)
			}

			// Compare text output (lines).
			oldLines := sortedLines(oldResp.Text)
			newLines := sortedLines(newResp.Text)
			if len(oldLines) != len(newLines) {
				t.Fatalf("tree lines: old=%d new=%d\nold:\n%s\nnew:\n%s",
					len(oldLines), len(newLines), oldResp.Text, newResp.Text)
			}
			for i := range oldLines {
				if oldLines[i] != newLines[i] {
					t.Fatalf("tree line mismatch at %d:\nold: %q\nnew: %q\nold full:\n%s\nnew full:\n%s",
						i, oldLines[i], newLines[i], oldResp.Text, newResp.Text)
				}
			}
		})
	}

	// Nonexistent: both should error.
	t.Run("nonexistent", func(t *testing.T) {
		_, oldErr := old.Tree(ctx, store.TreeRequest{Path: "/nope"})
		_, newErr := new.Tree(ctx, store.TreeRequest{Path: "/nope"})
		if oldErr == nil {
			t.Fatal("old Tree /nope: expected error")
		}
		if newErr == nil {
			t.Fatal("new Tree /nope: expected error")
		}
	})
}

func compareNonexistentRoot(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	// LS nonexistent.
	_, oldErr := old.LS(ctx, store.LSRequest{Path: "/nope"})
	_, newErr := new.LS(ctx, store.LSRequest{Path: "/nope"})
	if oldErr == nil || newErr == nil {
		t.Fatal("LS /nope: both should error for nonexistent root")
	}

	// Find nonexistent.
	_, oldErr = old.Find(ctx, store.FindRequest{Path: "/nope"})
	_, newErr = new.Find(ctx, store.FindRequest{Path: "/nope"})
	if oldErr == nil || newErr == nil {
		t.Fatal("Find /nope: both should error for nonexistent root")
	}
}

func compareRelativePath(t *testing.T, ctx context.Context, old, new store.Adapter) {
	t.Helper()

	// Relative path without leading slash — cleanDocPath should normalize.
	// Cat "docs/readme.md" should be treated as "/docs/readme.md".
	oldResp, oldErr := old.Cat(ctx, store.CatRequest{Path: "docs/readme.md"})
	newResp, newErr := new.Cat(ctx, store.CatRequest{Path: "docs/readme.md"})

	// Both should either succeed or fail together.
	if (oldErr == nil) != (newErr == nil) {
		t.Fatalf("Cat 'docs/readme.md': old err=%v new err=%v", oldErr, newErr)
	}
	if oldErr == nil {
		if oldResp.Content != newResp.Content {
			t.Fatalf("Cat relative path content mismatch:\nold: %q\nnew: %q", oldResp.Content, newResp.Content)
		}
	}
}

// --- Utility helpers ---

func nodeSet(nodes []store.Node) map[string]string {
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.Path] = n.Kind
	}
	return m
}

func nodePaths(nodes []store.Node) []string {
	paths := make([]string, len(nodes))
	for i, n := range nodes {
		paths[i] = n.Path
	}
	return paths
}

func sortedNodePaths(nodes []store.Node) []string {
	paths := nodePaths(nodes)
	sort.Strings(paths)
	return paths
}

func searchPaths(results []store.SearchResult) []string {
	paths := make([]string, len(results))
	for i, r := range results {
		paths[i] = r.Path
	}
	return paths
}

func hashMap(hashes []store.ContentHash) map[string]string {
	m := make(map[string]string, len(hashes))
	for _, h := range hashes {
		m[h.Path] = h.Hash
	}
	return m
}

func matchPaths(matches []store.Match) []string {
	paths := make([]string, len(matches))
	for i, m := range matches {
		paths[i] = fmt.Sprintf("%s:%d", m.Path, m.Line)
	}
	return paths
}

func matchSet(matches []store.Match) map[string]bool {
	m := make(map[string]bool, len(matches))
	for _, match := range matches {
		m[fmt.Sprintf("%s:%d:%s", match.Path, match.Line, strings.TrimSpace(match.Text))] = true
	}
	return m
}

func sortedLines(text string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	sort.Strings(lines)
	return lines
}

// TestDocAdapterMirrorRepo verifies DocAdapter works for a different repo
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
		Repo: "test-repo",
	}

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)
	seedBackfillData(t, containerName)

	if _, err := postgres.BackfillDocs(ctx, pool, cfg); err != nil {
		t.Fatalf("BackfillDocs: %v", err)
	}

	oldAdapter := postgres.New(pool, cfg)

	docAdapter := postgres.NewDocAdapter(pool, cfg)

	// Compare old vs new for mirror-repo.
	lsOld, err := oldAdapter.LS(ctx, store.LSRequest{Repo: "mirror-repo", Path: "/"})
	if err != nil {
		t.Fatalf("old LS mirror-repo: %v", err)
	}
	lsNew, err := docAdapter.LS(ctx, store.LSRequest{Repo: "mirror-repo", Path: "/"})
	if err != nil {
		t.Fatalf("new LS mirror-repo: %v", err)
	}
	if lsOld.Total != lsNew.Total {
		t.Fatalf("mirror LS total: old=%d new=%d", lsOld.Total, lsNew.Total)
	}

	// BatchHashes mirror-repo.
	oldHashes, err := oldAdapter.BatchHashes(ctx, store.HashRequest{Repo: "mirror-repo"})
	if err != nil {
		t.Fatalf("old BatchHashes mirror-repo: %v", err)
	}
	newHashes, err := docAdapter.BatchHashes(ctx, store.HashRequest{Repo: "mirror-repo"})
	if err != nil {
		t.Fatalf("new BatchHashes mirror-repo: %v", err)
	}
	if len(oldHashes.Hashes) != len(newHashes.Hashes) {
		t.Fatalf("mirror BatchHashes: old=%d new=%d", len(oldHashes.Hashes), len(newHashes.Hashes))
	}
}
