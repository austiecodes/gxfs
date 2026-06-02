-- Phase 1A: Document-centric schema (parallel to existing path-centric tables)
-- These tables are created empty and populated by backfill.

-- Core document table: one row per logical file (not per content blob).
-- legacy_path tracks the original vfs_nodes path for idempotent backfill.
-- Nullable: future docs created outside the legacy migration have no legacy_path.
create table if not exists {{.DocsTable}} (
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
create unique index if not exists idx_docs_legacy_path on {{.DocsTable}} (legacy_path) where legacy_path is not null;

-- Full-text search GIN index on content_search
create index if not exists idx_docs_content_search on {{.DocsTable}} using gin (content_search);

-- Index on content_hash for BatchHashes lookups
create index if not exists idx_docs_content_hash on {{.DocsTable}} (content_hash);

-- Repo → Doc mapping: replaces vfs_nodes + vfs_repo_nodes for file paths.
-- Directories are implicit from path prefix (no dir rows needed).
create table if not exists {{.RepoPathsTable}} (
    repo text not null,
    path text not null,
    doc_id uuid not null references {{.DocsTable}}(id),
    size bigint not null default 0,
    mtime timestamptz not null default now(),
    primary key (repo, path)
);

-- Index for prefix queries (LS/Find/BatchHashes)
create index if not exists idx_repo_paths_prefix on {{.RepoPathsTable}} (repo, path text_pattern_ops);

-- Index for finding all paths pointing to a doc (for orphan detection)
create index if not exists idx_repo_paths_doc_id on {{.RepoPathsTable}} (doc_id);

-- First-class docs namespaces: independent writable views over shared docs.
create table if not exists {{.DocNamespacesTable}} (
    name text primary key,
    description text not null default '',
    writable bool not null default false,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- Namespace → Doc mapping: directories are implicit from path prefix.
create table if not exists {{.DocNamespacePathsTable}} (
    namespace text not null references {{.DocNamespacesTable}}(name) on delete cascade,
    path text not null,
    doc_id uuid not null references {{.DocsTable}}(id),
    size bigint not null default 0,
    mtime timestamptz not null default now(),
    primary key (namespace, path)
);

-- Index for namespace prefix queries.
create index if not exists idx_doc_namespace_paths_prefix on {{.DocNamespacePathsTable}} (namespace, path text_pattern_ops);

-- Index for finding namespace paths pointing to a doc (for orphan detection).
create index if not exists idx_doc_namespace_paths_doc_id on {{.DocNamespacePathsTable}} (doc_id);

-- Docsets: empty tables, no API in Phase 1A
create table if not exists {{.DocsetsTable}} (
    id uuid primary key default gen_random_uuid(),
    name text not null unique,
    description text not null default '',
    visibility text not null default 'private',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists {{.DocsetDocsTable}} (
    docset_id uuid not null references {{.DocsetsTable}}(id),
    doc_id uuid not null references {{.DocsTable}}(id),
    path text not null,
    primary key (docset_id, path),
    unique (docset_id, doc_id)
);
