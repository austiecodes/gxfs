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

			// DocAdapter (via backfill) computes hashes for all files, including
			// ones that had NULL hash in the old tables. So new >= old count.
			if len(newResp.Hashes) < len(oldResp.Hashes) {
				t.Fatalf("hash count: old=%d new=%d (new should have >= old)", len(oldResp.Hashes), len(newResp.Hashes))
			}

			// Every old hash must be present in new. New may have extra
			// (backfill-computed) hashes not in old.
			oldMap := hashMap(oldResp.Hashes)
			newMap := hashMap(newResp.Hashes)
			for p, h := range oldMap {
				if newMap[p] != h {
					t.Fatalf("hash mismatch for %q: old=%q new=%q", p, h, newMap[p])
				}
			}

			// All new hashes must be non-empty.
			for _, h := range newResp.Hashes {
				if h.Hash == "" {
					t.Fatalf("empty hash for %q", h.Path)
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

	// BatchHashes mirror-repo: new >= old (backfill computes NULL hashes).
	oldHashes, err := oldAdapter.BatchHashes(ctx, store.HashRequest{Repo: "mirror-repo"})
	if err != nil {
		t.Fatalf("old BatchHashes mirror-repo: %v", err)
	}
	newHashes, err := docAdapter.BatchHashes(ctx, store.HashRequest{Repo: "mirror-repo"})
	if err != nil {
		t.Fatalf("new BatchHashes mirror-repo: %v", err)
	}
	if len(newHashes.Hashes) < len(oldHashes.Hashes) {
		t.Fatalf("mirror BatchHashes: old=%d new=%d (new should have >= old)", len(oldHashes.Hashes), len(newHashes.Hashes))
	}
}

// TestDocAdapterWritePath verifies Put/Delete/Edit on DocAdapter work correctly.
func TestDocAdapterWritePath(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-doc-write-test-%d", pgPort)
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

	da := postgres.NewDocAdapter(pool, cfg)

	t.Run("PutNewFile", func(t *testing.T) {
		resp, err := da.Put(ctx, store.PutRequest{Path: "/new.txt", Content: "hello world"})
		if err != nil {
			t.Fatalf("Put new: %v", err)
		}
		if resp.Node.Kind != "file" {
			t.Fatalf("Put node kind = %q, want file", resp.Node.Kind)
		}
		if resp.Node.Hash == "" {
			t.Fatal("Put node hash is empty")
		}

		// Verify via Cat.
		cat, err := da.Cat(ctx, store.CatRequest{Path: "/new.txt"})
		if err != nil {
			t.Fatalf("Cat new file: %v", err)
		}
		if cat.Content != "hello world" {
			t.Fatalf("Cat content = %q, want %q", cat.Content, "hello world")
		}

		// Verify via Search.
		sr, err := da.Search(ctx, store.SearchRequest{Query: "hello world"})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if sr.Total < 1 {
			t.Fatal("Search 'hello world' found nothing after Put")
		}

		// Verify via BatchHashes.
		hr, err := da.BatchHashes(ctx, store.HashRequest{Path: "/new.txt"})
		if err != nil {
			t.Fatalf("BatchHashes: %v", err)
		}
		if len(hr.Hashes) != 1 {
			t.Fatalf("BatchHashes for /new.txt = %d, want 1", len(hr.Hashes))
		}

		// Verify via Stat.
		st, err := da.Stat(ctx, store.StatRequest{Path: "/new.txt"})
		if err != nil {
			t.Fatalf("Stat new file: %v", err)
		}
		if st.Node.Hash != resp.Node.Hash {
			t.Fatalf("Stat hash = %q, Put hash = %q", st.Node.Hash, resp.Node.Hash)
		}
	})

	t.Run("PutOverwrite", func(t *testing.T) {
		// Overwrite existing file.
		resp, err := da.Put(ctx, store.PutRequest{Path: "/README.md", Content: "Updated content"})
		if err != nil {
			t.Fatalf("Put overwrite: %v", err)
		}

		cat, err := da.Cat(ctx, store.CatRequest{Path: "/README.md"})
		if err != nil {
			t.Fatalf("Cat after overwrite: %v", err)
		}
		if cat.Content != "Updated content" {
			t.Fatalf("Cat content = %q, want %q", cat.Content, "Updated content")
		}
		// Hash should be updated.
		if cat.Hash == "sha256:abc123" {
			t.Fatal("Cat hash still has pre-set value, should be updated")
		}
		if cat.Hash != resp.Node.Hash {
			t.Fatalf("Cat hash = %q, Put hash = %q", cat.Hash, resp.Node.Hash)
		}
	})

	t.Run("Edit", func(t *testing.T) {
		// Edit the file we just overwrote.
		editResp, err := da.Edit(ctx, store.EditRequest{Path: "/README.md", Old: "Updated", New: "Modified"})
		if err != nil {
			t.Fatalf("Edit: %v", err)
		}
		if editResp.Replaced < 1 {
			t.Fatalf("Edit replaced = %d, want >= 1", editResp.Replaced)
		}

		cat, err := da.Cat(ctx, store.CatRequest{Path: "/README.md"})
		if err != nil {
			t.Fatalf("Cat after edit: %v", err)
		}
		if cat.Content != "Modified content" {
			t.Fatalf("Cat content = %q, want %q", cat.Content, "Modified content")
		}

		// Verify Search picks up the edit.
		sr, err := da.Search(ctx, store.SearchRequest{Query: "Modified"})
		if err != nil {
			t.Fatalf("Search after edit: %v", err)
		}
		if sr.Total < 1 {
			t.Fatal("Search 'Modified' found nothing after Edit")
		}
	})

	t.Run("DeleteFile", func(t *testing.T) {
		// Delete a file.
		_, err := da.Delete(ctx, store.DeleteRequest{Path: "/new.txt"})
		if err != nil {
			t.Fatalf("Delete file: %v", err)
		}

		// Verify 404.
		_, err = da.Cat(ctx, store.CatRequest{Path: "/new.txt"})
		if err == nil {
			t.Fatal("Cat after delete: expected error")
		}
	})

	t.Run("DeleteNonexistent", func(t *testing.T) {
		_, err := da.Delete(ctx, store.DeleteRequest{Path: "/nonexistent.txt"})
		if err == nil {
			t.Fatal("Delete nonexistent: expected error")
		}
	})

	t.Run("DeleteDir", func(t *testing.T) {
		// Delete a directory recursively.
		_, err := da.Delete(ctx, store.DeleteRequest{Path: "/docs"})
		if err != nil {
			t.Fatalf("Delete dir: %v", err)
		}

		// Verify all files under /docs are gone.
		for _, p := range []string{"/docs/readme.md", "/docs/api/reference.md", "/docs/api/guide.md"} {
			_, err := da.Cat(ctx, store.CatRequest{Path: p})
			if err == nil {
				t.Fatalf("Cat %s after dir delete: expected error", p)
			}
		}

		// Verify LS / no longer shows docs.
		ls, err := da.LS(ctx, store.LSRequest{Path: "/"})
		if err != nil {
			t.Fatalf("LS / after dir delete: %v", err)
		}
		for _, n := range ls.Nodes {
			if n.Name == "docs" {
				t.Fatal("LS / still shows 'docs' after dir delete")
			}
		}
	})

	t.Run("RevisionIncrement", func(t *testing.T) {
		// Put a new file — revision should be 1.
		_, err := da.Put(ctx, store.PutRequest{Path: "/revision-test.txt", Content: "v1"})
		if err != nil {
			t.Fatalf("Put revision-test: %v", err)
		}

		// Check revision via direct query.
		docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")
		pathsTable := quoteTableForTest(cfg.Schema, "gxfs_repo_paths")
		var rev1 int
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			"select d.revision from %s rp join %s d on rp.doc_id = d.id where rp.repo = 'test-repo' and rp.path = '/revision-test.txt'",
			pathsTable, docsTable,
		)).Scan(&rev1); err != nil {
			t.Fatalf("get revision 1: %v", err)
		}
		if rev1 != 1 {
			t.Fatalf("initial revision = %d, want 1", rev1)
		}

		// Edit — revision should increment.
		_, err = da.Edit(ctx, store.EditRequest{Path: "/revision-test.txt", Old: "v1", New: "v2"})
		if err != nil {
			t.Fatalf("Edit revision-test: %v", err)
		}
		var rev2 int
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			"select d.revision from %s rp join %s d on rp.doc_id = d.id where rp.repo = 'test-repo' and rp.path = '/revision-test.txt'",
			pathsTable, docsTable,
		)).Scan(&rev2); err != nil {
			t.Fatalf("get revision 2: %v", err)
		}
		if rev2 != 2 {
			t.Fatalf("after edit revision = %d, want 2", rev2)
		}

		// Overwrite Put — revision should increment again.
		_, err = da.Put(ctx, store.PutRequest{Path: "/revision-test.txt", Content: "v3"})
		if err != nil {
			t.Fatalf("Put overwrite revision-test: %v", err)
		}
		var rev3 int
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			"select d.revision from %s rp join %s d on rp.doc_id = d.id where rp.repo = 'test-repo' and rp.path = '/revision-test.txt'",
			pathsTable, docsTable,
		)).Scan(&rev3); err != nil {
			t.Fatalf("get revision 3: %v", err)
		}
		if rev3 != 3 {
			t.Fatalf("after overwrite revision = %d, want 3", rev3)
		}
	})

	t.Run("EditNonexistent", func(t *testing.T) {
		_, err := da.Edit(ctx, store.EditRequest{Path: "/nope.txt", Old: "a", New: "b"})
		if err == nil {
			t.Fatal("Edit nonexistent: expected error")
		}
	})

	t.Run("DocPreservedAfterDelete", func(t *testing.T) {
		// Put a file, delete it, verify doc still exists (orphan).
		_, err := da.Put(ctx, store.PutRequest{Path: "/orphan-test.txt", Content: "will be orphaned"})
		if err != nil {
			t.Fatalf("Put orphan-test: %v", err)
		}

		// Get the doc_id.
		pathsTable := quoteTableForTest(cfg.Schema, "gxfs_repo_paths")
		docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")
		var docID string
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			"select d.id::text from %s rp join %s d on rp.doc_id = d.id where rp.repo = 'test-repo' and rp.path = '/orphan-test.txt'",
			pathsTable, docsTable,
		)).Scan(&docID); err != nil {
			t.Fatalf("get doc_id: %v", err)
		}

		// Delete.
		_, err = da.Delete(ctx, store.DeleteRequest{Path: "/orphan-test.txt"})
		if err != nil {
			t.Fatalf("Delete orphan-test: %v", err)
		}

		// Verify repo_path is gone.
		var count int
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			"select count(*) from %s where repo = 'test-repo' and path = '/orphan-test.txt'",
			pathsTable,
		)).Scan(&count); err != nil {
			t.Fatalf("count paths: %v", err)
		}
		if count != 0 {
			t.Fatalf("repo_path still exists after delete (count=%d)", count)
		}

		// Verify doc still exists (orphan).
		var docCount int
		if err := pool.QueryRow(ctx, fmt.Sprintf(
			"select count(*) from %s where id::text = '%s'",
			docsTable, docID,
		)).Scan(&docCount); err != nil {
			t.Fatalf("count docs: %v", err)
		}
		if docCount != 1 {
			t.Fatalf("doc was deleted (should be preserved as orphan), count=%d", docCount)
		}
	})

	t.Run("GrepNewContent", func(t *testing.T) {
		// Put a file, then grep for its content.
		_, err := da.Put(ctx, store.PutRequest{Path: "/grep-target.txt", Content: "line one\nfindme here\nline three"})
		if err != nil {
			t.Fatalf("Put grep-target: %v", err)
		}

		resp, err := da.Grep(ctx, store.GrepRequest{Path: "/", Pattern: "findme"})
		if err != nil {
			t.Fatalf("Grep findme: %v", err)
		}
		found := false
		for _, m := range resp.Matches {
			if m.Path == "/grep-target.txt" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("Grep did not find /grep-target.txt")
		}
	})

	t.Run("TreeAfterWrite", func(t *testing.T) {
		// Put files in a new dir, verify Tree shows them.
		_, err := da.Put(ctx, store.PutRequest{Path: "/newdir/file1.txt", Content: "f1"})
		if err != nil {
			t.Fatalf("Put newdir/file1: %v", err)
		}
		_, err = da.Put(ctx, store.PutRequest{Path: "/newdir/file2.txt", Content: "f2"})
		if err != nil {
			t.Fatalf("Put newdir/file2: %v", err)
		}

		resp, err := da.Tree(ctx, store.TreeRequest{Path: "/newdir"})
		if err != nil {
			t.Fatalf("Tree /newdir: %v", err)
		}
		if !strings.Contains(resp.Text, "file1.txt") || !strings.Contains(resp.Text, "file2.txt") {
			t.Fatalf("Tree /newdir text missing files:\n%s", resp.Text)
		}
	})
}
