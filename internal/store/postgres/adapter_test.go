package postgres

import (
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
