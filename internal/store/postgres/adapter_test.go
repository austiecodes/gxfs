package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/austiecodes/gxfs/internal/store"
	"github.com/austiecodes/gxfs/internal/vfs"
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

-- First-class docs namespaces: independent writable views over shared docs.
create table if not exists "public"."gxfs_doc_namespaces" (
    name text primary key,
    description text not null default '',
    writable bool not null default false,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- Namespace → Doc mapping: directories are implicit from path prefix.
create table if not exists "public"."gxfs_doc_namespace_paths" (
    namespace text not null references "public"."gxfs_doc_namespaces"(name) on delete cascade,
    path text not null,
    doc_id uuid not null references "public"."gxfs_docs"(id),
    size bigint not null default 0,
    mtime timestamptz not null default now(),
    primary key (namespace, path)
);

-- Index for namespace prefix queries.
create index if not exists idx_doc_namespace_paths_prefix on "public"."gxfs_doc_namespace_paths" (namespace, path text_pattern_ops);

-- Index for finding namespace paths pointing to a doc (for orphan detection).
create index if not exists idx_doc_namespace_paths_doc_id on "public"."gxfs_doc_namespace_paths" (doc_id);

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
		`-- DB-backed repository registry.
create table if not exists "public"."gxfs_repos" (
    name text primary key,
    writable bool not null default false,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    check (name <> '')
);

-- Preserve existing repo namespaces discovered from document path bindings.
insert into "public"."gxfs_repos" (name)
select distinct repo
from "public"."gxfs_repo_paths"
where repo <> ''
on conflict (name) do nothing;`,
		`create table if not exists "public"."gxfs_usage_events" (
    id uuid primary key default gen_random_uuid(),
    created_at timestamptz not null default now(),
    log_id text,
    session_id text,
    client_repo text,
    command text not null,
    exit_code integer not null,
    duration_ms bigint not null,
    event_kind text not null default 'cli.command',
    payload jsonb not null default '{}'::jsonb
);

create index if not exists idx_usage_events_created_at on "public"."gxfs_usage_events" (created_at);
create index if not exists idx_usage_events_log_id on "public"."gxfs_usage_events" (log_id) where log_id is not null;
create index if not exists idx_usage_events_session_id on "public"."gxfs_usage_events" (session_id) where session_id is not null;
create index if not exists idx_usage_events_client_repo on "public"."gxfs_usage_events" (client_repo) where client_repo is not null;
create index if not exists idx_usage_events_command on "public"."gxfs_usage_events" (command);`,
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

	// Find the doc tables migration.
	var docStmt string
	for _, statement := range statements {
		if strings.Contains(statement, `"gxfs_docs"`) {
			docStmt = statement
			break
		}
	}
	if docStmt == "" {
		t.Fatalf("SchemaSQL() missing doc tables migration: %v", statements)
	}

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
	if !strings.Contains(docStmt, `"gxfs_doc_namespaces"`) {
		t.Fatalf("empty schema migration missing \"gxfs_doc_namespaces\"")
	}
	if !strings.Contains(docStmt, `"gxfs_doc_namespace_paths"`) {
		t.Fatalf("empty schema migration missing \"gxfs_doc_namespace_paths\"")
	}
	if !strings.Contains(docStmt, `"gxfs_collections"`) {
		t.Fatalf("empty schema migration missing \"gxfs_collections\"")
	}

	// Must NOT start with a dot (e.g., `"gxfs_docs"` not `.gxfs_docs`).
	if strings.Contains(docStmt, ".\"gxfs_docs\"") {
		// Double-check it's not schema-qualified (which is wrong for empty schema).
		t.Fatalf("empty schema migration has schema-qualified table ref: contains .\"gxfs_docs\"")
	}

	var repoRegistryStmt string
	for _, statement := range statements {
		if strings.Contains(statement, `"gxfs_repos"`) {
			repoRegistryStmt = statement
			break
		}
	}
	if repoRegistryStmt == "" {
		t.Fatalf("SchemaSQL() missing repo registry migration: %v", statements)
	}
	if strings.Contains(repoRegistryStmt, ".\"gxfs_repos\"") {
		t.Fatalf("empty schema migration has schema-qualified repo registry table ref: contains .\"gxfs_repos\"")
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

// --- Doc query SQL builder tests ---

func testDocConfig() Config {
	return Config{Schema: "docschema"}
}

func testDocNamespaceConfig() Config {
	cfg := testDocConfig()
	cfg.DocBinding = DocBindingConfig{
		PathsTable:  docNamespacePathsTable,
		ScopeColumn: docNamespaceScopeColumn,
	}
	return cfg
}

func TestDocListPathsSQL(t *testing.T) {
	sql, err := DocListPathsSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocListPathsSQL() error = %v", err)
	}

	want := `select path, size, mtime from "docschema"."gxfs_repo_paths" where repo = $1 and path like $2 order by path`
	if sql != want {
		t.Fatalf("DocListPathsSQL() = %q, want %q", sql, want)
	}
}

func TestDocListPathsSQLNoSchema(t *testing.T) {
	sql, err := DocListPathsSQL(Config{})
	if err != nil {
		t.Fatalf("DocListPathsSQL() error = %v", err)
	}

	want := `select path, size, mtime from "gxfs_repo_paths" where repo = $1 and path like $2 order by path`
	if sql != want {
		t.Fatalf("DocListPathsSQL() = %q, want %q", sql, want)
	}
}

func TestDocNamespaceBindingSQL(t *testing.T) {
	cfg := testDocNamespaceConfig()
	for _, tt := range []struct {
		name       string
		fn         func(Config) (string, error)
		wantScope  string
		wantExtras []string
	}{
		{"DocListPathsSQL", DocListPathsSQL, "where namespace = $1", nil},
		{"DocCatSQL", DocCatSQL, "where rp.namespace = $1", nil},
		{"DocStatSQL", DocStatSQL, "where rp.namespace = $1", nil},
		{"DocStatDirSQL", DocStatDirSQL, "where namespace = $1", nil},
		{"DocSearchCountSQL", DocSearchCountSQL, "where rp.namespace = $1", nil},
		{"DocSearchDataSQL", DocSearchDataSQL, "where rp.namespace = $1", nil},
		{"DocLocateCountSQL", DocLocateCountSQL, "where rp.namespace = $1", nil},
		{"DocLocateDataSQL", DocLocateDataSQL, "where rp.namespace = $1", nil},
		{"DocBatchHashesSQL", DocBatchHashesSQL, "where rp.namespace = $1", nil},
		{"DocStreamGrepSQL", DocStreamGrepSQL, "where rp.namespace = $1", nil},
		{"DocUpdateByPathSQL", DocUpdateByPathSQL, "where rp.namespace = $1", nil},
		{"DocSelectForUpdateSQL", DocSelectForUpdateSQL, "where rp.namespace = $1", nil},
		{"DocUpsertPathSQL", DocUpsertPathSQL, "insert into", []string{"(namespace, path, doc_id, size, mtime)", "on conflict(namespace, path)"}},
		{"DocLookupPathSQL", DocLookupPathSQL, "where namespace = $1", nil},
		{"DocLookupPathWithHashSQL", DocLookupPathWithHashSQL, "where p.namespace = $1", nil},
		{"DocDeletePathSQL", DocDeletePathSQL, "where namespace = $1", nil},
		{"DocDeletePathRecursiveSQL", DocDeletePathRecursiveSQL, "where namespace = $1", nil},
		{"DocGlobCountSQL", DocGlobCountSQL, "where namespace = $1", nil},
		{"DocGlobDataSQL", DocGlobDataSQL, "where namespace = $1", nil},
		{"DocGlobDataAllSQL", DocGlobDataAllSQL, "where namespace = $1", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sql, err := tt.fn(cfg)
			if err != nil {
				t.Fatalf("%s() error = %v", tt.name, err)
			}
			if !strings.Contains(sql, `"docschema"."gxfs_doc_namespace_paths"`) {
				t.Fatalf("%s() missing docs namespace table: %q", tt.name, sql)
			}
			if !strings.Contains(sql, tt.wantScope) {
				t.Fatalf("%s() missing namespace scope %q: %q", tt.name, tt.wantScope, sql)
			}
			for _, want := range tt.wantExtras {
				if !strings.Contains(sql, want) {
					t.Fatalf("%s() missing %q: %q", tt.name, want, sql)
				}
			}
			for _, bad := range []string{`"gxfs_repo_paths"`, " repo = $1", "rp.repo = $1", "p.repo = $1", "on conflict(repo, path)"} {
				if strings.Contains(sql, bad) {
					t.Fatalf("%s() still contains repo binding %q: %q", tt.name, bad, sql)
				}
			}
		})
	}
}

func TestDocCatSQL(t *testing.T) {
	sql, err := DocCatSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocCatSQL() error = %v", err)
	}

	want := `select d.content, d.content_hash from "docschema"."gxfs_repo_paths" rp join "docschema"."gxfs_docs" d on rp.doc_id = d.id where rp.repo = $1 and rp.path = $2`
	if sql != want {
		t.Fatalf("DocCatSQL() = %q, want %q", sql, want)
	}
}

func TestDocStatSQL(t *testing.T) {
	sql, err := DocStatSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocStatSQL() error = %v", err)
	}

	want := `select rp.path, rp.size, rp.mtime, d.content_hash from "docschema"."gxfs_repo_paths" rp join "docschema"."gxfs_docs" d on rp.doc_id = d.id where rp.repo = $1 and rp.path = $2`
	if sql != want {
		t.Fatalf("DocStatSQL() = %q, want %q", sql, want)
	}
}

func TestDocStatDirSQL(t *testing.T) {
	sql, err := DocStatDirSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocStatDirSQL() error = %v", err)
	}

	want := `select count(*) from "docschema"."gxfs_repo_paths" where repo = $1 and path like $2`
	if sql != want {
		t.Fatalf("DocStatDirSQL() = %q, want %q", sql, want)
	}
}

func TestDocSearchCountSQL(t *testing.T) {
	sql, err := DocSearchCountSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocSearchCountSQL() error = %v", err)
	}

	// Must use plainto_tsquery and tsvector match.
	if !strings.Contains(sql, "plainto_tsquery") {
		t.Fatalf("DocSearchCountSQL() missing plainto_tsquery: %q", sql)
	}
	if !strings.Contains(sql, `@@ query`) {
		t.Fatalf("DocSearchCountSQL() missing @@ query match: %q", sql)
	}
	// Must include path filter.
	if !strings.Contains(sql, `$3 = ''`) {
		t.Fatalf("DocSearchCountSQL() missing path filter: %q", sql)
	}
	// Tables must be schema-qualified.
	if !strings.Contains(sql, `"docschema"."gxfs_repo_paths"`) {
		t.Fatalf("DocSearchCountSQL() missing schema-qualified paths table: %q", sql)
	}
	if !strings.Contains(sql, `"docschema"."gxfs_docs"`) {
		t.Fatalf("DocSearchCountSQL() missing schema-qualified docs table: %q", sql)
	}
}

func TestDocSearchDataSQL(t *testing.T) {
	sql, err := DocSearchDataSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocSearchDataSQL() error = %v", err)
	}

	// Must include rank, snippet, limit/offset.
	if !strings.Contains(sql, "ts_rank_cd") {
		t.Fatalf("DocSearchDataSQL() missing ts_rank_cd: %q", sql)
	}
	if !strings.Contains(sql, "ts_headline") {
		t.Fatalf("DocSearchDataSQL() missing ts_headline: %q", sql)
	}
	if !strings.Contains(sql, "limit $4 offset $5") {
		t.Fatalf("DocSearchDataSQL() missing limit/offset: %q", sql)
	}
}

func TestDocBatchHashesSQL(t *testing.T) {
	sql, err := DocBatchHashesSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocBatchHashesSQL() error = %v", err)
	}

	want := `select rp.path, d.content_hash from "docschema"."gxfs_repo_paths" rp join "docschema"."gxfs_docs" d on rp.doc_id = d.id ` +
		`where rp.repo = $1 and d.content_hash is not null ` +
		`and ($2 = '' or rp.path = $2 or rp.path like $2 || '/%%') ` +
		`order by rp.path`
	if sql != want {
		t.Fatalf("DocBatchHashesSQL() =\n%s\nwant:\n%s", sql, want)
	}
}

func TestDocStreamGrepSQL(t *testing.T) {
	sql, err := DocStreamGrepSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocStreamGrepSQL() error = %v", err)
	}

	want := `select rp.path, d.content from "docschema"."gxfs_repo_paths" rp join "docschema"."gxfs_docs" d on rp.doc_id = d.id ` +
		`where rp.repo = $1 and rp.path like $2 ` +
		`order by rp.path`
	if sql != want {
		t.Fatalf("DocStreamGrepSQL() =\n%s\nwant:\n%s", sql, want)
	}
}

func TestDocQuerySQLRejectsUnsafeSchema(t *testing.T) {
	bad := Config{Schema: "drop table users; --"}
	for _, fn := range []struct {
		name string
		fn   func(Config) (string, error)
	}{
		{"DocListPathsSQL", DocListPathsSQL},
		{"DocCatSQL", DocCatSQL},
		{"DocStatSQL", DocStatSQL},
		{"DocStatDirSQL", DocStatDirSQL},
		{"DocSearchCountSQL", DocSearchCountSQL},
		{"DocSearchDataSQL", DocSearchDataSQL},
		{"DocLocateCountSQL", DocLocateCountSQL},
		{"DocLocateDataSQL", DocLocateDataSQL},
		{"DocBatchHashesSQL", DocBatchHashesSQL},
		{"DocStreamGrepSQL", DocStreamGrepSQL},
		{"DocInsertSQL", DocInsertSQL},
		{"DocUpdateByPathSQL", DocUpdateByPathSQL},
		{"DocSelectForUpdateSQL", DocSelectForUpdateSQL},
		{"DocUpdateByIDSQL", DocUpdateByIDSQL},
		{"DocUpsertPathSQL", DocUpsertPathSQL},
		{"DocLookupPathSQL", DocLookupPathSQL},
		{"DocLookupPathWithHashSQL", DocLookupPathWithHashSQL},
		{"DocDeletePathSQL", DocDeletePathSQL},
		{"DocDeletePathRecursiveSQL", DocDeletePathRecursiveSQL},
		{"DocGlobCountSQL", DocGlobCountSQL},
		{"DocGlobDataSQL", DocGlobDataSQL},
		{"DocGlobDataAllSQL", DocGlobDataAllSQL},
	} {
		_, err := fn.fn(bad)
		if err == nil {
			t.Fatalf("%s() error = nil for unsafe schema, want rejection", fn.name)
		}
	}
}

func TestDocQuerySQLRejectsUnsafeBinding(t *testing.T) {
	for _, cfg := range []Config{
		{DocBinding: DocBindingConfig{PathsTable: "gxfs_repo_paths; drop table users", ScopeColumn: docRepoScopeColumn}},
		{DocBinding: DocBindingConfig{PathsTable: docRepoPathsTable, ScopeColumn: "repo; drop table users"}},
		{DocBinding: DocBindingConfig{PathsTable: docRepoPathsTable}},
		{DocBinding: DocBindingConfig{ScopeColumn: docRepoScopeColumn}},
	} {
		if _, err := DocListPathsSQL(cfg); err == nil {
			t.Fatalf("DocListPathsSQL(%+v) error = nil, want rejection", cfg.DocBinding)
		}
	}
}

// --- Doc write SQL builder tests ---

func TestDocInsertSQL(t *testing.T) {
	sql, err := DocInsertSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocInsertSQL() error = %v", err)
	}
	if !strings.Contains(sql, "returning id") {
		t.Fatalf("DocInsertSQL() missing returning id: %q", sql)
	}
	if !strings.Contains(sql, `"docschema"."gxfs_docs"`) {
		t.Fatalf("DocInsertSQL() missing schema-qualified table: %q", sql)
	}
}

func TestDocUpdateByPathSQL(t *testing.T) {
	sql, err := DocUpdateByPathSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocUpdateByPathSQL() error = %v", err)
	}
	if !strings.Contains(sql, "revision = revision + 1") {
		t.Fatalf("DocUpdateByPathSQL() missing revision increment: %q", sql)
	}
	if !strings.Contains(sql, "for update") {
		// Not FOR UPDATE — this is a direct update via join.
	}
}

func TestDocSelectForUpdateSQL(t *testing.T) {
	sql, err := DocSelectForUpdateSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocSelectForUpdateSQL() error = %v", err)
	}
	if !strings.Contains(sql, "for update of d") {
		t.Fatalf("DocSelectForUpdateSQL() missing FOR UPDATE: %q", sql)
	}
}

func TestDocUpsertPathSQL(t *testing.T) {
	sql, err := DocUpsertPathSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocUpsertPathSQL() error = %v", err)
	}
	if !strings.Contains(sql, "on conflict(repo, path)") {
		t.Fatalf("DocUpsertPathSQL() missing ON CONFLICT: %q", sql)
	}
}

func TestDocDeletePathRecursiveSQL(t *testing.T) {
	sql, err := DocDeletePathRecursiveSQL(testDocConfig())
	if err != nil {
		t.Fatalf("DocDeletePathRecursiveSQL() error = %v", err)
	}
	if !strings.Contains(sql, "path like $2 || '/%%'") {
		t.Fatalf("DocDeletePathRecursiveSQL() missing LIKE prefix: %q", sql)
	}
}

func TestDocAdapterConstructorsSetBindingMode(t *testing.T) {
	repoAdapter := NewDocAdapter(nil, Config{Repo: "repo"})
	if repoAdapter.cfg.DocBinding.PathsTable != docRepoPathsTable {
		t.Fatalf("NewDocAdapter paths table = %q, want %q", repoAdapter.cfg.DocBinding.PathsTable, docRepoPathsTable)
	}
	if repoAdapter.cfg.DocBinding.ScopeColumn != docRepoScopeColumn {
		t.Fatalf("NewDocAdapter scope column = %q, want %q", repoAdapter.cfg.DocBinding.ScopeColumn, docRepoScopeColumn)
	}

	namespaceAdapter := NewDocsNamespaceAdapter(nil, Config{Repo: "docs"})
	if namespaceAdapter.cfg.DocBinding.PathsTable != docNamespacePathsTable {
		t.Fatalf("NewDocsNamespaceAdapter paths table = %q, want %q", namespaceAdapter.cfg.DocBinding.PathsTable, docNamespacePathsTable)
	}
	if namespaceAdapter.cfg.DocBinding.ScopeColumn != docNamespaceScopeColumn {
		t.Fatalf("NewDocsNamespaceAdapter scope column = %q, want %q", namespaceAdapter.cfg.DocBinding.ScopeColumn, docNamespaceScopeColumn)
	}
}

// --- DocAdapter cache tests ---

func TestDocAdapterCacheHit(t *testing.T) {
	d := &DocAdapter{
		cfg:         Config{Repo: "test"},
		cachedTrees: make(map[string]*docCachedTree),
	}

	// Pre-populate cache.
	tree, err := vfs.New([]vfs.File{{Path: "/a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	d.cachedTrees["test"] = &docCachedTree{tree: tree, loadedAt: time.Now()}

	// treeFor should return cached tree (no pool needed).
	got, err := d.treeFor(context.TODO(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if got != tree {
		t.Fatal("treeFor did not return cached tree")
	}
}

func TestDocAdapterCacheExpiry(t *testing.T) {
	d := &DocAdapter{
		cfg:         Config{Repo: "test", CacheTTL: 1 * time.Millisecond},
		cachedTrees: make(map[string]*docCachedTree),
	}

	// Pre-populate expired cache.
	tree, err := vfs.New([]vfs.File{{Path: "/a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	d.cachedTrees["test"] = &docCachedTree{tree: tree, loadedAt: time.Now().Add(-1 * time.Second)}

	// treeFor should detect expiry — but without a pool it will fail.
	// That's fine: we just verify the expiry check works.
	if !d.cacheExpired(d.cachedTrees["test"]) {
		t.Fatal("cache should be expired")
	}
}

func TestDocAdapterCacheNoExpiry(t *testing.T) {
	d := &DocAdapter{
		cfg:         Config{Repo: "test"}, // CacheTTL = 0 → never expire
		cachedTrees: make(map[string]*docCachedTree),
	}

	tree, err := vfs.New([]vfs.File{{Path: "/a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	d.cachedTrees["test"] = &docCachedTree{tree: tree, loadedAt: time.Now().Add(-1 * time.Hour)}

	if d.cacheExpired(d.cachedTrees["test"]) {
		t.Fatal("cache with TTL=0 should never expire")
	}
}

func TestDocAdapterInvalidate(t *testing.T) {
	d := &DocAdapter{
		cfg:         Config{Repo: "test"},
		cachedTrees: make(map[string]*docCachedTree),
	}

	tree, err := vfs.New([]vfs.File{{Path: "/a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	d.cachedTrees["test"] = &docCachedTree{tree: tree, loadedAt: time.Now()}

	d.Invalidate()

	if len(d.cachedTrees) != 0 {
		t.Fatalf("Invalidate left %d entries", len(d.cachedTrees))
	}
}

func TestDocAdapterInvalidateRepo(t *testing.T) {
	d := &DocAdapter{
		cfg:         Config{Repo: "test"},
		cachedTrees: make(map[string]*docCachedTree),
	}

	tree1, _ := vfs.New([]vfs.File{{Path: "/a.txt"}})
	tree2, _ := vfs.New([]vfs.File{{Path: "/b.txt"}})
	d.cachedTrees["repo1"] = &docCachedTree{tree: tree1, loadedAt: time.Now()}
	d.cachedTrees["repo2"] = &docCachedTree{tree: tree2, loadedAt: time.Now()}

	d.invalidateRepo("repo1")

	if _, ok := d.cachedTrees["repo1"]; ok {
		t.Fatal("repo1 should be invalidated")
	}
	if _, ok := d.cachedTrees["repo2"]; !ok {
		t.Fatal("repo2 should still be cached")
	}
}

func TestDocAdapterConcurrentTreeFor(t *testing.T) {
	d := &DocAdapter{
		cfg:         Config{Repo: "test"},
		cachedTrees: make(map[string]*docCachedTree),
	}

	// Pre-populate.
	tree, err := vfs.New([]vfs.File{{Path: "/a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	d.cachedTrees["test"] = &docCachedTree{tree: tree, loadedAt: time.Now()}

	// Concurrent reads should all get the same cached tree.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := d.treeFor(context.TODO(), "test")
			if err != nil {
				t.Error(err)
				return
			}
			if got != tree {
				t.Error("concurrent treeFor did not return cached tree")
			}
		}()
	}
	wg.Wait()
}
