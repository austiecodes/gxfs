//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gxfs/internal/store"
	"gxfs/internal/store/postgres"
)

// TestBackfillDocsIntegration verifies BackfillDocs migrates legacy data
// correctly into the new document-centric tables and is idempotent.
func TestBackfillDocsIntegration(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-backfill-test-%d", pgPort)
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
	}

	// Step 1: Create schema and seed legacy data.
	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)
	seedBackfillData(t, containerName)

	// Step 2: Run BackfillDocs first time.
	result1, err := postgres.BackfillDocs(ctx, pool, cfg)
	if err != nil {
		t.Fatalf("BackfillDocs (run 1): %v", err)
	}

	// 5 unique docs (one per file), 8 repo_paths (5 test-repo + 3 mirror-repo)
	if result1.DocsInserted != 5 {
		t.Fatalf("run 1: DocsInserted = %d, want 5", result1.DocsInserted)
	}
	if result1.PathsInserted != 8 {
		t.Fatalf("run 1: PathsInserted = %d, want 8", result1.PathsInserted)
	}
	// 3 files have NULL hash → computed
	if result1.HashesComputed != 3 {
		t.Fatalf("run 1: HashesComputed = %d, want 3", result1.HashesComputed)
	}

	// Step 3: Verify doc contents.
	verifyDocContents(t, ctx, pool, cfg)

	// Step 4: Verify repo_paths.
	verifyRepoPaths(t, ctx, pool, cfg)

	// Step 5: Verify duplicate content gets separate doc_ids.
	verifyNoContentDedup(t, ctx, pool, cfg)

	// Step 6: Verify computed hashes are correct.
	verifyComputedHashes(t, ctx, pool, cfg)

	// Step 7: Capture doc_ids, run BackfillDocs again, verify idempotency.
	docIDs1 := captureDocIDs(t, ctx, pool, cfg)
	result2, err := postgres.BackfillDocs(ctx, pool, cfg)
	if err != nil {
		t.Fatalf("BackfillDocs (run 2): %v", err)
	}
	if result2.DocsInserted != 5 {
		t.Fatalf("run 2: DocsInserted = %d, want 5 (idempotent)", result2.DocsInserted)
	}
	if result2.PathsInserted != 8 {
		t.Fatalf("run 2: PathsInserted = %d, want 8 (idempotent)", result2.PathsInserted)
	}

	docIDs2 := captureDocIDs(t, ctx, pool, cfg)
	if len(docIDs1) != len(docIDs2) {
		t.Fatalf("doc count changed: run1=%d, run2=%d", len(docIDs1), len(docIDs2))
	}
	for path, id1 := range docIDs1 {
		id2, ok := docIDs2[path]
		if !ok {
			t.Fatalf("run 2: doc for %s disappeared", path)
		}
		if id1 != id2 {
			t.Fatalf("doc_id for %s changed: %v → %v (not idempotent)", path, id1, id2)
		}
	}

	// Step 8: Verify no duplicate rows.
	verifyNoDuplicates(t, ctx, pool, cfg)

	// Step 9: Verification queries — compare old vs new results.
	verifyCatEquivalent(t, ctx, pool, cfg)
	verifyBatchHashesEquivalent(t, ctx, pool, cfg)
	verifyLSEquivalent(t, ctx, pool, cfg)
	verifyFindEquivalent(t, ctx, pool, cfg)
	verifySearchEquivalent(t, ctx, pool, cfg)
}

// seedBackfillData creates legacy tables with specific scenarios:
//   - duplicate content (README.md and /docs/api/reference.md both have short content)
//   - duplicate basename (two readme.md files in different dirs)
//   - two repos sharing some files
//   - NULL content_hash on 3 of 5 files
//   - dirs that should NOT be migrated
func seedBackfillData(t *testing.T, containerName string) {
	t.Helper()

	const sql = `
create table if not exists vfs_nodes (
    path text primary key,
    kind text not null default 'file',
    size bigint not null default 0,
    updated_at timestamptz not null default now(),
    check (kind in ('file', 'dir'))
);

create table if not exists vfs_content (
    path text primary key references vfs_nodes(path) on delete cascade,
    content text not null default ''
);

create table if not exists vfs_repo_nodes (
    repo text not null,
    path text not null references vfs_nodes(path) on delete cascade,
    primary key (repo, path)
);

-- Directories (should NOT appear in gxfs_docs)
insert into vfs_nodes(path, kind, size, updated_at) values
    ('/docs', 'dir', 0, '2026-01-01T00:00:00Z'),
    ('/docs/api', 'dir', 0, '2026-01-01T00:00:00Z'),
    ('/src', 'dir', 0, '2026-01-01T00:00:00Z')
on conflict do nothing;

-- Files with specific content
insert into vfs_nodes(path, kind, size, updated_at) values
    ('/README.md', 'file', 17, '2026-01-01T00:00:00Z'),
    ('/docs/readme.md', 'file', 42, '2026-01-02T00:00:00Z'),
    ('/docs/api/reference.md', 'file', 14, '2026-01-04T00:00:00Z'),
    ('/docs/api/guide.md', 'file', 12, '2026-01-05T00:00:00Z'),
    ('/src/main.go', 'file', 28, '2026-01-03T00:00:00Z')
on conflict(path) do update set kind = excluded.kind, size = excluded.size, updated_at = excluded.updated_at;

-- Content: README.md and guide.md have SAME content to test no-dedup
-- /docs/readme.md and /docs/api/readme.md would share basename but different content
insert into vfs_content(path, content) values
    ('/README.md', 'Hello World' || chr(10)),
    ('/docs/readme.md', '# Docs readme' || chr(10)),
    ('/docs/api/reference.md', 'API reference' || chr(10)),
    ('/docs/api/guide.md', 'Hello World' || chr(10)),  -- same content as README.md
    ('/src/main.go', 'package main' || chr(10) || 'func main() {}' || chr(10))
on conflict(path) do update set content = excluded.content;

-- Add content_hash: NULL for 3 files, set for 2
alter table vfs_content add column if not exists content_hash text;
update vfs_content set content_hash = 'sha256:abc123' where path = '/README.md';
update vfs_content set content_hash = 'sha256:def456' where path = '/src/main.go';
-- /docs/readme.md, /docs/api/reference.md, /docs/api/guide.md → NULL hash

-- Repo 'test-repo' has all files
insert into vfs_repo_nodes(repo, path) values
    ('test-repo', '/docs'),
    ('test-repo', '/docs/api'),
    ('test-repo', '/src'),
    ('test-repo', '/README.md'),
    ('test-repo', '/docs/readme.md'),
    ('test-repo', '/docs/api/reference.md'),
    ('test-repo', '/docs/api/guide.md'),
    ('test-repo', '/src/main.go')
on conflict do nothing;

-- Repo 'mirror-repo' has a subset (tests multi-repo same file)
insert into vfs_repo_nodes(repo, path) values
    ('mirror-repo', '/docs'),
    ('mirror-repo', '/docs/api'),
    ('mirror-repo', '/README.md'),
    ('mirror-repo', '/docs/readme.md'),
    ('mirror-repo', '/docs/api/guide.md')
on conflict do nothing;
`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, err := run(ctx, "", strings.NewReader(sql),
		"docker", "exec", "-i", containerName,
		"psql", "-U", "gxfs", "-d", "gxfs", "-v", "ON_ERROR_STOP=1",
	)
	if err != nil {
		t.Fatalf("seed backfill data: %v: %s", err, output)
	}
}

func connectPool(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	statements, err := postgres.SchemaSQL(cfg)
	if err != nil {
		t.Fatalf("SchemaSQL: %v", err)
	}
	for _, stmt := range statements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec migration: %v\nstmt: %s", err, stmt)
		}
	}
}

func verifyDocContents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")

	// Check doc count: should be 5 (one per file, not per content blob, dirs excluded).
	var docCount int
	if err := pool.QueryRow(ctx, fmt.Sprintf("select count(*) from %s", docsTable)).Scan(&docCount); err != nil {
		t.Fatalf("count docs: %v", err)
	}
	if docCount != 5 {
		t.Fatalf("doc count = %d, want 5", docCount)
	}

	// Check titles (basename of legacy_path).
	rows, err := pool.Query(ctx, fmt.Sprintf(
		"select legacy_path, title from %s order by legacy_path", docsTable,
	))
	if err != nil {
		t.Fatalf("query docs: %v", err)
	}
	defer rows.Close()

	expected := map[string]string{
		"/README.md":             "README.md",
		"/docs/readme.md":        "readme.md",
		"/docs/api/reference.md": "reference.md",
		"/docs/api/guide.md":     "guide.md",
		"/src/main.go":           "main.go",
	}
	for rows.Next() {
		var legacyPath, title string
		if err := rows.Scan(&legacyPath, &title); err != nil {
			t.Fatalf("scan doc: %v", err)
		}
		wantTitle, ok := expected[legacyPath]
		if !ok {
			t.Fatalf("unexpected doc with legacy_path %q", legacyPath)
		}
		if title != wantTitle {
			t.Fatalf("title for %q = %q, want %q", legacyPath, title, wantTitle)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate docs: %v", err)
	}

	// Check no dir rows.
	var dirCount int
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select count(*) from %s where legacy_path in ('/docs', '/docs/api', '/src')",
		docsTable,
	)).Scan(&dirCount); err != nil {
		t.Fatalf("count dir docs: %v", err)
	}
	if dirCount != 0 {
		t.Fatalf("found %d dir docs, want 0 (dirs should not be in gxfs_docs)", dirCount)
	}

	// Check revision = 1 for all import snapshots.
	var nonRevision1 int
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select count(*) from %s where revision != 1", docsTable,
	)).Scan(&nonRevision1); err != nil {
		t.Fatalf("check revision: %v", err)
	}
	if nonRevision1 != 0 {
		t.Fatalf("found %d docs with revision != 1 (backfill is import snapshot, revision stays 1)", nonRevision1)
	}
}

func verifyRepoPaths(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	pathsTable := quoteTableForTest(cfg.Schema, "gxfs_repo_paths")

	// Check repo_paths count.
	var pathCount int
	if err := pool.QueryRow(ctx, fmt.Sprintf("select count(*) from %s", pathsTable)).Scan(&pathCount); err != nil {
		t.Fatalf("count repo_paths: %v", err)
	}
	// test-repo: 5 files; mirror-repo: 3 files = 8 total
	if pathCount != 8 {
		t.Fatalf("repo_paths count = %d, want 8", pathCount)
	}

	// Check test-repo paths.
	var testRepoCount int
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select count(*) from %s where repo = 'test-repo'", pathsTable,
	)).Scan(&testRepoCount); err != nil {
		t.Fatalf("count test-repo paths: %v", err)
	}
	if testRepoCount != 5 {
		t.Fatalf("test-repo paths = %d, want 5", testRepoCount)
	}

	// Check mirror-repo paths.
	var mirrorRepoCount int
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select count(*) from %s where repo = 'mirror-repo'", pathsTable,
	)).Scan(&mirrorRepoCount); err != nil {
		t.Fatalf("count mirror-repo paths: %v", err)
	}
	if mirrorRepoCount != 3 {
		t.Fatalf("mirror-repo paths = %d, want 3", mirrorRepoCount)
	}
}

func verifyNoContentDedup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")

	// README.md and guide.md have same content. They must have different doc_ids.
	var sameContentCount int
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select count(*) from %s where legacy_path in ('/README.md', '/docs/api/guide.md')",
		docsTable,
	)).Scan(&sameContentCount); err != nil {
		t.Fatalf("count same-content docs: %v", err)
	}
	if sameContentCount != 2 {
		t.Fatalf("same-content docs = %d, want 2 (no content dedup)", sameContentCount)
	}
}

func verifyComputedHashes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")

	// Files with NULL hash in old table should now have computed hash.
	rows, err := pool.Query(ctx, fmt.Sprintf(
		"select legacy_path, content_hash from %s where legacy_path in ('/docs/readme.md', '/docs/api/reference.md', '/docs/api/guide.md')",
		docsTable,
	))
	if err != nil {
		t.Fatalf("query computed hashes: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			t.Fatalf("scan hash: %v", err)
		}
		if hash == "" {
			t.Fatalf("content_hash for %q is empty, want computed hash", path)
		}
		if hash == "sha256:abc123" || hash == "sha256:def456" {
			t.Fatalf("content_hash for %q = %q (looks like wrong pre-set hash)", path, hash)
		}
		// Verify hash format.
		if len(hash) < 10 || hash[:7] != "sha256:" {
			t.Fatalf("content_hash for %q = %q, want sha256:hex format", path, hash)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate hashes: %v", err)
	}

	// Pre-set hashes should be preserved.
	var readmeHash, mainHash string
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select content_hash from %s where legacy_path = '/README.md'", docsTable,
	)).Scan(&readmeHash); err != nil {
		t.Fatalf("get README hash: %v", err)
	}
	if readmeHash != "sha256:abc123" {
		t.Fatalf("README hash = %q, want sha256:abc123 (pre-set)", readmeHash)
	}

	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select content_hash from %s where legacy_path = '/src/main.go'", docsTable,
	)).Scan(&mainHash); err != nil {
		t.Fatalf("get main.go hash: %v", err)
	}
	if mainHash != "sha256:def456" {
		t.Fatalf("main.go hash = %q, want sha256:def456 (pre-set)", mainHash)
	}
}

func captureDocIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) map[string]string {
	t.Helper()

	docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")

	rows, err := pool.Query(ctx, fmt.Sprintf(
		"select legacy_path, id::text from %s where legacy_path is not null", docsTable,
	))
	if err != nil {
		t.Fatalf("capture doc ids: %v", err)
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var path, id string
		if err := rows.Scan(&path, &id); err != nil {
			t.Fatalf("scan doc id: %v", err)
		}
		m[path] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate doc ids: %v", err)
	}
	return m
}

func verifyNoDuplicates(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")

	// Verify no duplicate legacy_path rows.
	var dupCount int
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select count(*) from (select legacy_path, count(*) as c from %s where legacy_path is not null group by legacy_path having count(*) > 1) dup",
		docsTable,
	)).Scan(&dupCount); err != nil {
		t.Fatalf("check duplicate docs: %v", err)
	}
	if dupCount != 0 {
		t.Fatalf("found %d duplicate legacy_path rows", dupCount)
	}
}

func verifyCatEquivalent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")
	pathsTable := quoteTableForTest(cfg.Schema, "gxfs_repo_paths")

	// For each file in test-repo, verify Cat-equivalent query returns correct content.
	rows, err := pool.Query(ctx, fmt.Sprintf(
		"select rp.path, d.content from %s rp join %s d on rp.doc_id = d.id where rp.repo = 'test-repo' order by rp.path",
		pathsTable, docsTable,
	))
	if err != nil {
		t.Fatalf("cat-equivalent query: %v", err)
	}
	defer rows.Close()

	expected := map[string]string{
		"/README.md":             "Hello World\n",
		"/docs/readme.md":        "# Docs readme\n",
		"/docs/api/reference.md": "API reference\n",
		"/docs/api/guide.md":     "Hello World\n",
		"/src/main.go":           "package main\nfunc main() {}\n",
	}

	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			t.Fatalf("scan cat result: %v", err)
		}
		wantContent, ok := expected[path]
		if !ok {
			t.Fatalf("unexpected cat path %q", path)
		}
		if content != wantContent {
			t.Fatalf("cat content for %q = %q, want %q", path, content, wantContent)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate cat results: %v", err)
	}
}

func verifyBatchHashesEquivalent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")
	pathsTable := quoteTableForTest(cfg.Schema, "gxfs_repo_paths")

	// Verify BatchHashes-equivalent: every file has a non-empty content_hash.
	rows, err := pool.Query(ctx, fmt.Sprintf(
		"select rp.path, d.content_hash from %s rp join %s d on rp.doc_id = d.id where rp.repo = 'test-repo' and d.content_hash is not null order by rp.path",
		pathsTable, docsTable,
	))
	if err != nil {
		t.Fatalf("batch-hashes query: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			t.Fatalf("scan hash result: %v", err)
		}
		if hash == "" {
			t.Fatalf("empty hash for %q", path)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate hash results: %v", err)
	}
	if count != 5 {
		t.Fatalf("batch-hashes count = %d, want 5 (all files should have hash)", count)
	}

	// Verify computed hashes match store.HashContent.
	// Check the /docs/readme.md file whose hash was NULL → computed.
	var readmeContent string
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select content from %s where legacy_path = '/docs/readme.md'", docsTable,
	)).Scan(&readmeContent); err != nil {
		t.Fatalf("get readme content: %v", err)
	}

	var readmeHash string
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		"select content_hash from %s where legacy_path = '/docs/readme.md'", docsTable,
	)).Scan(&readmeHash); err != nil {
		t.Fatalf("get readme hash: %v", err)
	}

	expectedHash := store.HashContent(readmeContent)
	if readmeHash != expectedHash {
		t.Fatalf("computed hash for /docs/readme.md = %q, want %q", readmeHash, expectedHash)
	}
}

func verifyLSEquivalent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	pathsTable := quoteTableForTest(cfg.Schema, "gxfs_repo_paths")

	// LS / should list top-level entries: implicit dirs /docs, /src + file /README.md.
	rows, err := pool.Query(ctx, fmt.Sprintf(
		"select path from %s where repo = 'test-repo' order by path", pathsTable,
	))
	if err != nil {
		t.Fatalf("ls query: %v", err)
	}
	var allPaths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan ls path: %v", err)
		}
		allPaths = append(allPaths, p)
	}
	rows.Close()

	// Derive implicit dirs and top-level LS entries.
	lsRoot := lsEntries(t, allPaths, "/")
	expectedRoot := map[string]bool{"/docs": true, "/src": true, "/README.md": true}
	if len(lsRoot) != len(expectedRoot) {
		t.Fatalf("LS / = %v, want %d entries matching %v", lsRoot, len(expectedRoot), expectedRoot)
	}
	for _, entry := range lsRoot {
		if !expectedRoot[entry] {
			t.Fatalf("LS / unexpected entry %q", entry)
		}
	}

	// LS /docs should list /docs/readme.md + implicit dir /docs/api.
	lsDocs := lsEntries(t, allPaths, "/docs")
	expectedDocs := map[string]bool{"/docs/readme.md": true, "/docs/api": true}
	if len(lsDocs) != len(expectedDocs) {
		t.Fatalf("LS /docs = %v, want %d entries matching %v", lsDocs, len(expectedDocs), expectedDocs)
	}
	for _, entry := range lsDocs {
		if !expectedDocs[entry] {
			t.Fatalf("LS /docs unexpected entry %q", entry)
		}
	}

	// LS /docs/api should list files only.
	lsAPI := lsEntries(t, allPaths, "/docs/api")
	expectedAPI := map[string]bool{"/docs/api/reference.md": true, "/docs/api/guide.md": true}
	if len(lsAPI) != len(expectedAPI) {
		t.Fatalf("LS /docs/api = %v, want %d entries matching %v", lsAPI, len(expectedAPI), expectedAPI)
	}
	for _, entry := range lsAPI {
		if !expectedAPI[entry] {
			t.Fatalf("LS /docs/api unexpected entry %q", entry)
		}
	}
}

// lsEntries derives LS-style directory listing from flat file paths.
// Returns immediate children (files and implicit dirs) under the given prefix.
func lsEntries(t *testing.T, allPaths []string, prefix string) []string {
	t.Helper()
	seen := make(map[string]bool)
	prefix = strings.TrimRight(prefix, "/") + "/"
	for _, p := range allPaths {
		if !strings.HasPrefix(p, prefix) || p == prefix {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		parts := strings.SplitN(rest, "/", 2)
		child := prefix + parts[0]
		seen[child] = true
	}
	var entries []string
	for e := range seen {
		entries = append(entries, e)
	}
	return entries
}

func verifyFindEquivalent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	pathsTable := quoteTableForTest(cfg.Schema, "gxfs_repo_paths")

	// Find all files named "readme.md" in test-repo.
	rows, err := pool.Query(ctx, fmt.Sprintf(
		"select path from %s where repo = 'test-repo' and path like '%%/readme.md' or (repo = 'test-repo' and path = '/readme.md') order by path",
		pathsTable,
	))
	if err != nil {
		t.Fatalf("find query: %v", err)
	}
	var found []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan find result: %v", err)
		}
		found = append(found, p)
	}
	rows.Close()
	// Should find /docs/readme.md only (not /README.md which is different name)
	if len(found) != 1 || found[0] != "/docs/readme.md" {
		t.Fatalf("find readme.md = %v, want [/docs/readme.md]", found)
	}

	// Find all .md files in test-repo.
	rows2, err := pool.Query(ctx, fmt.Sprintf(
		"select path from %s where repo = 'test-repo' and path like '%%%%.md' order by path",
		pathsTable,
	))
	if err != nil {
		t.Fatalf("find *.md query: %v", err)
	}
	var mdFiles []string
	for rows2.Next() {
		var p string
		if err := rows2.Scan(&p); err != nil {
			t.Fatalf("scan find md result: %v", err)
		}
		mdFiles = append(mdFiles, p)
	}
	rows2.Close()
	// test-repo has: /README.md, /docs/readme.md, /docs/api/reference.md, /docs/api/guide.md
	if len(mdFiles) != 4 {
		t.Fatalf("find *.md = %v, want 4 files", mdFiles)
	}
}

func verifySearchEquivalent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) {
	t.Helper()

	docsTable := quoteTableForTest(cfg.Schema, "gxfs_docs")
	pathsTable := quoteTableForTest(cfg.Schema, "gxfs_repo_paths")

	// Search for "main" — should hit /src/main.go.
	rows, err := pool.Query(ctx, fmt.Sprintf(
		"select rp.repo, rp.path, ts_rank_cd(d.content_search, query) as rank "+
			"from %s rp join %s d on rp.doc_id = d.id, "+
			"plainto_tsquery('english', 'main') as query "+
			"where d.content_search @@ query "+
			"order by rank desc",
		pathsTable, docsTable,
	))
	if err != nil {
		t.Fatalf("search query: %v", err)
	}
	type searchHit struct {
		repo string
		path string
	}
	var hits []searchHit
	for rows.Next() {
		var hit searchHit
		if err := rows.Scan(&hit.repo, &hit.path, new(float64)); err != nil {
			t.Fatalf("scan search result: %v", err)
		}
		hits = append(hits, hit)
	}
	rows.Close()

	// "main" should match "func main" in /src/main.go for test-repo.
	// mirror-repo doesn't have main.go, so only 1 hit.
	if len(hits) != 1 {
		t.Fatalf("search 'main' hits = %d, want 1: %+v", len(hits), hits)
	}
	if hits[0].repo != "test-repo" || hits[0].path != "/src/main.go" {
		t.Fatalf("search 'main' hit = %s/%s, want test-repo//src/main.go", hits[0].repo, hits[0].path)
	}

	// Search for "hello" — should hit README.md and guide.md (both have "Hello World").
	rows2, err := pool.Query(ctx, fmt.Sprintf(
		"select rp.repo, rp.path from %s rp join %s d on rp.doc_id = d.id, "+
			"plainto_tsquery('english', 'hello') as query "+
			"where d.content_search @@ query "+
			"order by rp.repo, rp.path",
		pathsTable, docsTable,
	))
	if err != nil {
		t.Fatalf("search 'hello' query: %v", err)
	}
	var helloHits []string
	for rows2.Next() {
		var repo, path string
		if err := rows2.Scan(&repo, &path); err != nil {
			t.Fatalf("scan hello hit: %v", err)
		}
		helloHits = append(helloHits, repo+"/"+path)
	}
	rows2.Close()
	// "hello" matches README.md and guide.md.
	// test-repo has both → 2 hits. mirror-repo has both → 2 hits. Total 4.
	if len(helloHits) != 4 {
		t.Fatalf("search 'hello' hits = %d, want 4: %v", len(helloHits), helloHits)
	}
}

func quoteTableForTest(schema, table string) string {
	if schema != "" {
		return fmt.Sprintf(`"%s"."%s"`, schema, table)
	}
	return fmt.Sprintf(`"%s"`, table)
}
