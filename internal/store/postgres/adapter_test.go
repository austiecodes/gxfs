package postgres

import (
	"strings"
	"testing"
	"time"

	"gxfs/internal/store"
)

var _ store.Adapter = (*Adapter)(nil)

func TestListNodesSQLJoinsRepoNodes(t *testing.T) {
	sql, err := ListNodesSQL(Config{
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
	})
	if err != nil {
		t.Fatalf("ListNodesSQL() error = %v", err)
	}

	want := `select n."path", n."kind", n."size", n."updated_at" from "public"."vfs_nodes" n join "public"."vfs_repo_nodes" r on n."path" = r."path" where r.repo = $1 order by n."path"`
	if sql != want {
		t.Fatalf("ListNodesSQL() = %q, want %q", sql, want)
	}
}

func TestListNodesSQLUsesNullableMTimeWhenUnconfigured(t *testing.T) {
	sql, err := ListNodesSQL(Config{
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn: "path",
			KindColumn: "kind",
			SizeColumn: "size",
		},
	})
	if err != nil {
		t.Fatalf("ListNodesSQL() error = %v", err)
	}

	want := `select n."path", n."kind", n."size", null::timestamptz from "public"."vfs_nodes" n join "public"."vfs_repo_nodes" r on n."path" = r."path" where r.repo = $1 order by n."path"`
	if sql != want {
		t.Fatalf("ListNodesSQL() = %q, want %q", sql, want)
	}
}

func TestListNodesSQLRejectsUnsafeIdentifier(t *testing.T) {
	_, err := ListNodesSQL(Config{
		Schema:         "public",
		NodesTable:     `vfs_nodes; drop table users;`,
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn: "path",
			KindColumn: "kind",
		},
	})
	if err == nil {
		t.Fatal("ListNodesSQL() error = nil, want unsafe identifier rejection")
	}
}

func TestContentQueriesUseConfiguredPathColumn(t *testing.T) {
	cfg := Config{
		Schema:         "public",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn: "file_path",
		},
	}

	load, err := LoadContentSQL(cfg)
	if err != nil {
		t.Fatalf("LoadContentSQL() error = %v", err)
	}
	if want := `select content from "public"."vfs_content" where "file_path" = $1`; load != want {
		t.Fatalf("LoadContentSQL() = %q, want %q", load, want)
	}

	upsert, err := UpsertContentSQL(cfg)
	if err != nil {
		t.Fatalf("UpsertContentSQL() error = %v", err)
	}
	if want := `insert into "public"."vfs_content"("file_path", content, content_hash) values($1, $2, $3) on conflict("file_path") do update set content = excluded.content, content_hash = excluded.content_hash`; upsert != want {
		t.Fatalf("UpsertContentSQL() = %q, want %q", upsert, want)
	}

	repo, err := UpsertRepoNodeSQL(cfg)
	if err != nil {
		t.Fatalf("UpsertRepoNodeSQL() error = %v", err)
	}
	if want := `insert into "public"."vfs_repo_nodes"(repo, "file_path") values($1, $2) on conflict do nothing`; repo != want {
		t.Fatalf("UpsertRepoNodeSQL() = %q, want %q", repo, want)
	}
}

func TestSchemaSQLCreatesConfiguredTables(t *testing.T) {
	statements, err := SchemaSQL(Config{
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
	})
	if err != nil {
		t.Fatalf("SchemaSQL() error = %v", err)
	}

	want := []string{
		`create schema if not exists "public"`,
		`create table if not exists "public"."vfs_nodes" (
    "path" text primary key,
    "kind" text not null default 'file',
    "size" bigint not null default 0,
    "updated_at" timestamptz not null default now(),
    check ("kind" in ('file', 'dir'))
)`,
		`create table if not exists "public"."vfs_content" (
    "path" text primary key references "public"."vfs_nodes"("path") on delete cascade,
    content text not null default ''
)`,
		`create table if not exists "public"."vfs_repo_nodes" (
    repo text not null,
    "path" text not null references "public"."vfs_nodes"("path") on delete cascade,
    primary key (repo, "path")
)`,
		`-- Full-text search: generated tsvector column on content + GIN index
alter table "public"."vfs_content" add column if not exists content_search tsvector
    generated always as (to_tsvector('english', coalesce(content, ''))) stored;

create index if not exists idx_content_search on "public"."vfs_content" using gin (content_search);`,
		`alter table "public"."vfs_content" add column if not exists content_hash text;`,
		`-- Phase 1A: Document-centric schema (parallel to existing path-centric tables)
-- These tables are created empty and populated by backfill.

-- Core document table: one row per logical file (not per content blob).
-- legacy_path tracks the original vfs_nodes path for idempotent backfill.
-- Nullable: future docs created outside the legacy migration have no legacy_path.
create table if not exists "public"."gxfs_docs" (
    id uuid primary key default gen_random_uuid(),
    legacy_path text,
    title text not null default '',
    content text not null default '',
    content_hash text not null,
    content_search tsvector generated always as (to_tsvector('english', coalesce(content, ''))) stored,
    revision bigint not null default 1,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- Idempotent backfill: unique legacy_path only where present.
create unique index if not exists idx_docs_legacy_path on "public"."gxfs_docs" (legacy_path) where legacy_path is not null;

-- Full-text search GIN index on content_search
create index if not exists idx_docs_content_search on "public"."gxfs_docs" using gin (content_search);

-- Index on content_hash for BatchHashes lookups
create index if not exists idx_docs_content_hash on "public"."gxfs_docs" (content_hash);

-- Repo → Doc mapping: replaces vfs_nodes + vfs_repo_nodes for file paths.
-- Directories are implicit from path prefix (no dir rows needed).
create table if not exists "public"."gxfs_repo_paths" (
    repo text not null,
    path text not null,
    doc_id uuid not null references "public"."gxfs_docs"(id),
    size bigint not null default 0,
    mtime timestamptz not null default now(),
    primary key (repo, path)
);

-- Index for prefix queries (LS/Find/BatchHashes)
create index if not exists idx_repo_paths_prefix on "public"."gxfs_repo_paths" (repo, path text_pattern_ops);

-- Index for finding all paths pointing to a doc (for orphan detection)
create index if not exists idx_repo_paths_doc_id on "public"."gxfs_repo_paths" (doc_id);

-- Collections: empty tables, no API in Phase 1A
create table if not exists "public"."gxfs_collections" (
    id uuid primary key default gen_random_uuid(),
    name text not null unique,
    description text not null default '',
    visibility text not null default 'private',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists "public"."gxfs_collection_docs" (
    collection_id uuid not null references "public"."gxfs_collections"(id),
    doc_id uuid not null references "public"."gxfs_docs"(id),
    path text not null,
    primary key (collection_id, path),
    unique (collection_id, doc_id)
);`,
	}
	if len(statements) != len(want) {
		t.Fatalf("SchemaSQL() len = %d, want %d: %v", len(statements), len(want), statements)
	}
	for i := range want {
		if statements[i] != want[i] {
			t.Fatalf("SchemaSQL()[%d] = %q, want %q", i, statements[i], want[i])
		}
	}
}

func TestSchemaSQLWithEmptySchemaRendersDocTablesWithoutLeadingDot(t *testing.T) {
	statements, err := SchemaSQL(Config{
		Schema:         "", // empty schema — should not produce ".gxfs_docs"
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
	})
	if err != nil {
		t.Fatalf("SchemaSQL() error = %v", err)
	}

	// Find the doc tables migration (last statement).
	docStmt := statements[len(statements)-1]

	// Must NOT contain ".gxfs_docs" (which would happen with empty SchemaName).
	if strings.Contains(docStmt, ".gxfs_docs") && !strings.Contains(docStmt, `".gxfs_docs"`) {
		t.Fatalf("empty schema migration contains bare .gxfs_docs — schema quoting broken")
	}

	// Must contain properly quoted table names without schema prefix.
	if !strings.Contains(docStmt, `"gxfs_docs"`) {
		t.Fatalf("empty schema migration missing \"gxfs_docs\" — got: %s", docStmt[:200])
	}
	if !strings.Contains(docStmt, `"gxfs_repo_paths"`) {
		t.Fatalf("empty schema migration missing \"gxfs_repo_paths\"")
	}
	if !strings.Contains(docStmt, `"gxfs_collections"`) {
		t.Fatalf("empty schema migration missing \"gxfs_collections\"")
	}

	// Must NOT start with a dot (e.g., `"gxfs_docs"` not `.gxfs_docs`).
	if strings.Contains(docStmt, ".\"gxfs_docs\"") {
		// Double-check it's not schema-qualified (which is wrong for empty schema).
		t.Fatalf("empty schema migration has schema-qualified table ref: contains .\"gxfs_docs\"")
	}
}

func TestAdapterCacheTTLZeroDoesNotExpire(t *testing.T) {
	adapter := &Adapter{}
	adapter.cfg.CacheTTL = 0
	cache := &cachedTree{loadedAt: time.Now().Add(-24 * time.Hour)}

	if adapter.expired(cache) {
		t.Fatal("expired() = true, want false when CacheTTL is zero")
	}
}

func TestAdapterCacheTTLPositiveExpires(t *testing.T) {
	adapter := &Adapter{}
	adapter.cfg.CacheTTL = time.Minute
	cache := &cachedTree{loadedAt: time.Now().Add(-2 * time.Minute)}

	if !adapter.expired(cache) {
		t.Fatal("expired() = false, want true when cache is older than TTL")
	}
}

func TestBackfillHashSQL(t *testing.T) {
	sql, err := BackfillHashSQL(Config{
		Schema:       "public",
		ContentTable: "vfs_content",
		Files: FileTableConfig{
			PathColumn: "path",
		},
	})
	if err != nil {
		t.Fatalf("BackfillHashSQL() error = %v", err)
	}
	want := `update "public"."vfs_content" set content_hash = $2 where "path" = $1 and content_hash is null`
	if sql != want {
		t.Fatalf("BackfillHashSQL() = %q, want %q", sql, want)
	}
}

func TestBackfillSourceSQLJoinsAllTables(t *testing.T) {
	sql, err := backfillSourceSQL(Config{
		Schema:         "myschema",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
	})
	if err != nil {
		t.Fatalf("backfillSourceSQL() error = %v", err)
	}

	want := `select r.repo, n."path", c.content, c.content_hash, n."size", n."updated_at" ` +
		`from "myschema"."vfs_nodes" n ` +
		`join "myschema"."vfs_content" c on n."path" = c."path" ` +
		`join "myschema"."vfs_repo_nodes" r on n."path" = r."path" ` +
		`where n."kind" = 'file' ` +
		`order by n."path"`
	if sql != want {
		t.Fatalf("backfillSourceSQL() =\n%s\nwant:\n%s", sql, want)
	}
}

func TestBackfillSourceSQLWithoutSizeOrMtime(t *testing.T) {
	sql, err := backfillSourceSQL(Config{
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn: "path",
			KindColumn: "kind",
		},
	})
	if err != nil {
		t.Fatalf("backfillSourceSQL() error = %v", err)
	}

	if !strings.Contains(sql, ", 0,") {
		t.Fatalf("expected '0' fallback for missing size column, got: %s", sql)
	}
	if !strings.Contains(sql, "now()") {
		t.Fatalf("expected 'now()' fallback for missing mtime column, got: %s", sql)
	}
}

func TestBackfillDocInsertSQL(t *testing.T) {
	sql, err := backfillDocInsertSQL(Config{Schema: "myschema"})
	if err != nil {
		t.Fatalf("backfillDocInsertSQL() error = %v", err)
	}

	want := `insert into "myschema"."gxfs_docs"(legacy_path, title, content, content_hash) ` +
		`values($1, $2, $3, $4) ` +
		`on conflict(legacy_path) where legacy_path is not null ` +
		`do update set title = excluded.title, content = excluded.content, ` +
		`content_hash = excluded.content_hash, updated_at = now() ` +
		`returning id`
	if sql != want {
		t.Fatalf("backfillDocInsertSQL() =\n%s\nwant:\n%s", sql, want)
	}
}

func TestBackfillPathInsertSQL(t *testing.T) {
	sql, err := backfillPathInsertSQL(Config{Schema: "myschema"})
	if err != nil {
		t.Fatalf("backfillPathInsertSQL() error = %v", err)
	}

	want := `insert into "myschema"."gxfs_repo_paths"(repo, path, doc_id, size, mtime) ` +
		`values($1, $2, $3, $4, $5) ` +
		`on conflict(repo, path) do update set doc_id = excluded.doc_id, size = excluded.size, mtime = excluded.mtime`
	if sql != want {
		t.Fatalf("backfillPathInsertSQL() =\n%s\nwant:\n%s", sql, want)
	}
}
