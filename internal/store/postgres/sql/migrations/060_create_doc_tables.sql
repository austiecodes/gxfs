-- Phase 1A: Document-centric schema (parallel to existing path-centric tables)
-- These tables are created empty and populated by backfill.

-- Core document table: one row per logical file (not per content blob).
-- legacy_path tracks the original vfs_nodes path for idempotent backfill.
create table if not exists {{.SchemaName}}.gxfs_docs (
    id uuid primary key default gen_random_uuid(),
    legacy_path text unique not null,
    title text not null default '',
    content text not null default '',
    content_hash text not null,
    content_search tsvector generated always as (to_tsvector('english', coalesce(content, ''))) stored,
    revision bigint not null default 1,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- Full-text search GIN index on content_search
create index if not exists idx_docs_content_search on {{.SchemaName}}.gxfs_docs using gin (content_search);

-- Index on content_hash for BatchHashes lookups
create index if not exists idx_docs_content_hash on {{.SchemaName}}.gxfs_docs (content_hash);

-- Repo → Doc mapping: replaces vfs_nodes + vfs_repo_nodes for file paths.
-- Directories are implicit from path prefix (no dir rows needed).
create table if not exists {{.SchemaName}}.gxfs_repo_paths (
    repo text not null,
    path text not null,
    doc_id uuid not null references {{.SchemaName}}.gxfs_docs(id),
    size bigint not null default 0,
    mtime timestamptz not null default now(),
    primary key (repo, path)
);

-- Index for prefix queries (LS/Find/BatchHashes)
create index if not exists idx_repo_paths_prefix on {{.SchemaName}}.gxfs_repo_paths (repo, path text_pattern_ops);

-- Index for finding all paths pointing to a doc (for orphan detection)
create index if not exists idx_repo_paths_doc_id on {{.SchemaName}}.gxfs_repo_paths (doc_id);

-- Collections: empty tables, no API in Phase 1A
create table if not exists {{.SchemaName}}.gxfs_collections (
    id uuid primary key default gen_random_uuid(),
    name text not null unique,
    description text not null default '',
    visibility text not null default 'private',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists {{.SchemaName}}.gxfs_collection_docs (
    collection_id uuid not null references {{.SchemaName}}.gxfs_collections(id),
    doc_id uuid not null references {{.SchemaName}}.gxfs_docs(id),
    path text not null,
    primary key (collection_id, path),
    unique (collection_id, doc_id)
);
