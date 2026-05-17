//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gxfs/internal/store/postgres"
)

// TestGCDryRunForce tests the GC dry-run and force modes.
func TestGCDryRunForce(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-gc-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:        dsn,
		Schema:     "public",
		Repo:       "test-repo",
		RepoTable:  "vfs_repo_nodes",
		NodesTable: "vfs_nodes",
	}

	// Setup: create schema
	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)

	// Insert orphan docs directly
	insertOrphanDocs(t, ctx, pool, 3)

	// Test 1: Dry-run should find orphans but not delete
	dryRunResult, err := postgres.GC(ctx, pool, "public", postgres.GCRequest{
		DryRun:     true,
		GraceHours: 0, // Set to 0 to catch docs created just now
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("GC dry-run: %v", err)
	}
	if dryRunResult.Count != 3 {
		t.Errorf("dry-run count = %d, want 3", dryRunResult.Count)
	}
	if len(dryRunResult.Candidates) != 3 {
		t.Errorf("dry-run candidates = %d, want 3", len(dryRunResult.Candidates))
	}

	// Verify orphans still exist after dry-run
	var countAfterDryRun int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM gxfs_docs WHERE id NOT IN (SELECT doc_id FROM gxfs_repo_paths WHERE doc_id IS NOT NULL)").Scan(&countAfterDryRun)
	if err != nil {
		t.Fatalf("count after dry-run: %v", err)
	}
	if countAfterDryRun != 3 {
		t.Errorf("docs after dry-run = %d, want 3 (dry-run should not delete)", countAfterDryRun)
	}

	// Test 2: Force should delete orphans
	forceResult, err := postgres.GC(ctx, pool, "public", postgres.GCRequest{
		DryRun:     false,
		GraceHours: 0,
	})
	if err != nil {
		t.Fatalf("GC force: %v", err)
	}
	if forceResult.Count != 3 {
		t.Errorf("force deleted = %d, want 3", forceResult.Count)
	}

	// Verify orphans are gone
	var countAfterForce int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM gxfs_docs WHERE id NOT IN (SELECT doc_id FROM gxfs_repo_paths WHERE doc_id IS NOT NULL)").Scan(&countAfterForce)
	if err != nil {
		t.Fatalf("count after force: %v", err)
	}
	if countAfterForce != 0 {
		t.Errorf("docs after force = %d, want 0", countAfterForce)
	}
}

// TestGCGracePeriod tests that grace period protects recent docs.
func TestGCGracePeriod(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-gc-grace-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:        dsn,
		Schema:     "public",
		Repo:       "test-repo",
		RepoTable:  "vfs_repo_nodes",
		NodesTable: "vfs_nodes",
	}

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)

	// Insert an orphan doc with old updated_at (simulating an old orphan)
	_, err := pool.Exec(ctx, `
		INSERT INTO gxfs_docs (id, title, content, content_hash, updated_at)
		VALUES (gen_random_uuid(), 'old-orphan', 'old content', 'hash1', NOW() - INTERVAL '2 hours')
	`)
	if err != nil {
		t.Fatalf("insert old orphan: %v", err)
	}

	// Insert an orphan doc with recent updated_at (should be protected by grace period)
	_, err = pool.Exec(ctx, `
		INSERT INTO gxfs_docs (id, title, content, content_hash, updated_at)
		VALUES (gen_random_uuid(), 'recent-orphan', 'recent content', 'hash2', NOW())
	`)
	if err != nil {
		t.Fatalf("insert recent orphan: %v", err)
	}

	// GC with 1 hour grace period should only delete the old one
	result, err := postgres.GC(ctx, pool, "public", postgres.GCRequest{
		DryRun:     false,
		GraceHours: 1,
	})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.Count != 1 {
		t.Errorf("deleted = %d, want 1 (grace period should protect recent)", result.Count)
	}

	// Verify recent doc still exists
	var remaining int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM gxfs_docs WHERE title = 'recent-orphan'").Scan(&remaining)
	if err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Errorf("recent doc remaining = %d, want 1", remaining)
	}
}

// TestGCCollectionRefs tests that docs referenced by collections are not deleted.
func TestGCCollectionRefs(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-gc-coll-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:        dsn,
		Schema:     "public",
		Repo:       "test-repo",
		RepoTable:  "vfs_repo_nodes",
		NodesTable: "vfs_nodes",
	}

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)

	// Create a collection
	var collectionID string
	err := pool.QueryRow(ctx, `
		INSERT INTO gxfs_collections (id, name, description, visibility)
		VALUES (gen_random_uuid(), 'test-collection', 'test', 'private')
		RETURNING id
	`).Scan(&collectionID)
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	// Create an orphan doc
	var docID string
	err = pool.QueryRow(ctx, `
		INSERT INTO gxfs_docs (id, title, content, content_hash, updated_at)
		VALUES (gen_random_uuid(), 'collection-doc', 'content', 'hash1', NOW() - INTERVAL '2 hours')
		RETURNING id
	`).Scan(&docID)
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}

	// Reference the doc from a collection
	_, err = pool.Exec(ctx, `
		INSERT INTO gxfs_collection_docs (collection_id, doc_id, path)
		VALUES ($1, $2, '/collection-doc.md')
	`, collectionID, docID)
	if err != nil {
		t.Fatalf("create collection_doc: %v", err)
	}

	// GC should NOT delete this doc (it's referenced by a collection)
	result, err := postgres.GC(ctx, pool, "public", postgres.GCRequest{
		DryRun:     true,
		GraceHours: 0, // Even with no grace, should be protected
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.Count != 0 {
		t.Errorf("orphan count = %d, want 0 (collection ref should protect)", result.Count)
	}
}

// TestGCRepoPathRefs tests that docs referenced by repo_paths are not deleted.
func TestGCRepoPathRefs(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-gc-repo-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:        dsn,
		Schema:     "public",
		Repo:       "test-repo",
		RepoTable:  "vfs_repo_nodes",
		NodesTable: "vfs_nodes",
	}

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)

	// Create a doc with a repo_path reference
	var docID string
	err := pool.QueryRow(ctx, `
		INSERT INTO gxfs_docs (id, title, content, content_hash, updated_at)
		VALUES (gen_random_uuid(), 'active-doc', 'content', 'hash1', NOW() - INTERVAL '2 hours')
		RETURNING id
	`).Scan(&docID)
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO gxfs_repo_paths (repo, path, doc_id, size, mtime)
		VALUES ('test-repo', '/active-doc.md', $1, 100, NOW())
	`, docID)
	if err != nil {
		t.Fatalf("create repo_path: %v", err)
	}

	// GC should NOT delete this doc (it's referenced by repo_paths)
	result, err := postgres.GC(ctx, pool, "public", postgres.GCRequest{
		DryRun:     true,
		GraceHours: 0,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.Count != 0 {
		t.Errorf("orphan count = %d, want 0 (repo_path ref should protect)", result.Count)
	}
}

// --- Helpers ---

func insertOrphanDocs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		_, err := pool.Exec(ctx, `
			INSERT INTO gxfs_docs (id, title, legacy_path, content, content_hash, updated_at)
			VALUES (gen_random_uuid(), $1, $2, 'orphan content', $3, NOW() - INTERVAL '2 hours')
		`, fmt.Sprintf("orphan-%d", i), fmt.Sprintf("/orphan-%d.md", i), fmt.Sprintf("hash-%d", i))
		if err != nil {
			t.Fatalf("insert orphan doc %d: %v", i, err)
		}
	}
}

func init() {
	// Ensure time package is imported for e2e tests
	_ = time.Now()
}
