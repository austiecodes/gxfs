# GXFS Cross-Repo Knowledge Filesystem Roadmap

## Background

GXFS exists because LLM agents need reusable project knowledge without paying
the context cost of many MCP tools. Agents are already good at Unix-style
discovery: `ls`, `tree`, `grep`, `cat`, and `find`. A repo-local `docs/`
directory plus links from `AGENTS.md` or `CLAUDE.md` gives agents gradual
exposure: start from a compact index, then read only the relevant docs.

The next problem is cross-repo reuse. Some knowledge belongs to a repo, but
some knowledge belongs to a library, framework, internal platform, or repeated
pitfall. For example, `openai-go/v3` gotchas should not be copied by hand into
every repo that uses that package. GXFS should let agents discover, mount, and
read those shared docs through a small CLI surface instead of adding more MCP
tools.

## Product Goal

GXFS should become an agent-friendly virtual documentation filesystem:

- The local repo has a small `.gxfs` configuration that points to a self-hosted
  server.
- Agents use familiar commands to discover and read remote docs.
- If a doc is not already mounted, agents can run `gxfs search` to discover
  shared docs and mount relevant results into the local repo view.
- The backend stores reusable docs once, then exposes them under repo-specific
  mount paths.
- Optional local materialization can pull selected remote docs into real local
  markdown files so Codex, Claude, and other tools can use their native file
  search/read abilities.

The recommended default is CLI-first with explicit materialization only for
selected mounts.

## Current Implementation Gaps

### CLI and Config

- `gxfs init` should work before `.gxfs/settings.toml` exists. Today command
  execution loads config before dispatching most commands, which makes init in a
  fresh repo fragile.
- Init should produce a self-hosting-friendly config template. The server
  address, repo name, auth mode, default docs root, cache policy, and mount
  examples should be easy to edit.
- `mount.include`, `mount.exclude`, and `docs.path` currently do not define a
  real local-to-remote mount model.
- There is no `gxfs sync` command to upload existing repo docs to the backend.
- There is no `gxfs search` command to discover docs outside the mounted view.
- Piping is only partially present through `gxfs write` stdin. Read commands are
  pipe-compatible because they print to stdout, but the CLI does not yet have
  polished workflows such as `gxfs search ... | gxfs mount --from-stdin` or
  structured output flags.

### Server and Multi-Repo

- The HTTP API shape has `/v1/repos/{repo}/{op}`, but the server currently
  supports exactly one configured repo.
- The Postgres adapter uses the configured repo internally instead of honoring
  the request repo throughout the store layer.
- There is no repo registry, namespace model, collection model, or policy for
  shared docs.
- Performance is acceptable for small docs, but `grep` currently loads content
  into the Go process and searches in memory. Large libraries need indexed
  search.

### VFS and Cache

- The tree model is useful, but it currently represents one repo view rather
  than a composed view made from multiple mounts.
- Cache TTL behavior needs correction: an empty TTL should mean "cache until
  invalidated", not "expire immediately".
- The cached tree is mutable and shared between requests. Content loading and
  writes need stronger synchronization or immutable snapshots.
- There is no local cache policy, revision tracking, stale detection, or
  materialized file lifecycle.

### Data Model

- Current storage is path-centric: `vfs_nodes(path)`, `vfs_content(path)`, and
  `vfs_repo_nodes(repo, path)`.
- Cross-repo reuse needs a stronger identity model. A shared doc can have one
  canonical identity but many mount paths.
- Path should be a view-level concern, not the primary identity of document
  content.

### Auth and Permissions

- There is no authentication, authorization, or write protection.
- Read-only shared docs and writable repo-owned docs need different policies.
- Destructive operations such as delete need permission checks and auditability.

## Configuration Direction

Use three local files with separate responsibilities.

### `.gxfs/settings.toml`

Human-edited base configuration. This file answers: "How does this repo talk to
GXFS?" It should stay stable, self-hosting-friendly, and safe to commit if it
contains no secrets.

```toml
version = 1
repo = "github.com/austiecodes/gxfs"

[server]
addr = "http://127.0.0.1:7635"

[auth]
mode = "bearer"
token_env = "GXFS_TOKEN"

[docs]
path = "docs"

[cache]
metadata_ttl = "5m"
content_ttl = "24h"
materialize = "explicit"
```

Design notes:

- `server.addr` keeps self-hosting first-class.
- Secrets are referenced through environment variables, not stored directly.
- This file should not be rewritten by ordinary `gxfs search` operations.
- This file should not contain generated file trees or discovered search
  results.

### `.gxfs/mounts.toml`

Human-readable desired mount state. This file answers: "Which remote knowledge
should this repo see, and where?" It may be edited by humans or updated by
commands such as `gxfs mount`.

```toml
version = 1

[[mounts]]
local = "docs"
remote = "repo://self/docs"
mode = "writable"

[[mounts]]
local = "docs/gotchas/openai-go"
remote = "collection://openai-go/v3/gotchas"
mode = "readonly"
source = "search"
```

Design notes:

- `local` is the path agents see.
- `remote` is a stable backend namespace reference.
- `mode` allows the CLI and server to distinguish writable repo docs from
  read-only shared docs.
- `source` records whether the mount was initialized by default config, search,
  sync, or a human edit.
- Search and mount workflows should update `mounts.toml`, not
  `settings.toml`.

### Remote Reference Namespace

Remote refs need a deliberate namespace contract before GXFS supports arbitrary
cross-repo mounts. Repo names and paths can both contain slashes, so a ref such
as `repo://github.com/openai/openai-go/docs/gotchas` does not clearly say where
the repo identity ends and the path begins.

Phase 1 should therefore support only the unambiguous current-repo form:

```toml
remote = "repo://self/docs"
```

Later phases should choose one explicit cross-repo syntax before enabling
non-self repo refs. Candidate forms:

```toml
# Explicit path query; easy to parse, URL-like.
remote = "repo://github.com/openai/openai-go?path=/docs/gotchas"

# Explicit delimiter between repo identity and path.
remote = "repo:github.com/openai/openai-go:/docs/gotchas"

# Structured fields instead of packing identity and path into one string.
remote_type = "repo"
remote_repo = "github.com/openai/openai-go"
remote_path = "/docs/gotchas"
```

Recommended direction:

- Use `repo://self/<path>` for the current repo's own docs.
- Use an explicit repo/path delimiter or structured fields if true cross-repo
  repo mounts are needed.
- Prefer `collection://...` for reusable library or platform knowledge, such as
  `collection://openai-go/v3/gotchas`, because these docs may combine official
  docs, internal notes, and gotchas from multiple sources.

### `.gxfs/manifest.toml`

Generated resolved state. This file answers: "What concrete files and revisions
does the current mount set resolve to?" It is safe to delete and rebuild.

```toml
version = 1
generated_at = "2026-05-11T00:00:00Z"

[[entries]]
local = "docs/gotchas/openai-go/f-function-is-not-available.md"
remote_doc = "doc://openai-go/v3/gotchas/f-function-is-not-available"
mount = "docs/gotchas/openai-go"
revision = "sha256:..."
size = 1234
mtime = "2026-05-10T00:00:00Z"
materialized = false
```

Design notes:

- The manifest is the local file tree cache the user described.
- `settings.toml` stays small and hand-editable.
- `mounts.toml` records intended mounts.
- `manifest.toml` can be rewritten by `gxfs sync refresh`,
  `gxfs sync materialize`, and mount resolution after `gxfs mount`.
- The CLI can use the manifest for fast local `tree/find/stat` output, then
  fetch content on demand.

## CLI Roadmap

### Phase 1: Fix Foundation

- Make `gxfs init` config-free. `init`, `help`, and `version` should not require
  an existing settings file.
- Expand `gxfs init` into a real template generator:
  - `--server http://...`
  - `--repo ...`
  - `--docs docs`
  - `--auth bearer|none`
  - `--agent agents|claude|none`
- Implement mount path resolution from `.gxfs/mounts.toml`.
- Apply include/exclude or replace them with explicit mounts.
- Fix cache TTL semantics.
- Add data race tests or run `go test -race ./...` as a regular verification
  target.

### Phase 2: Local Docs Sync

- Add `gxfs sync push docs` to upload existing local docs into the current repo
  namespace.
- Add `gxfs sync pull docs` to refresh remote docs into the manifest and,
  optionally, materialized files.
- Add conflict behavior:
  - default: fail on remote/local divergence
  - `--force-local`: local wins
  - `--force-remote`: remote wins
- Store content hash and remote revision in the manifest.

### Phase 3: Search and Mount

- Add `gxfs search <query>` for docs outside the current mounted view.
- Output should support both human text and machine-readable JSON:
  - `gxfs search "openai-go function call" --json`
  - `gxfs search "openai-go function call" | gxfs mount --interactive`
  - `gxfs mount add docs://openai-go-sdk/gotchas docs/gotchas/openai-go`
- Search should return document, collection, and suggested mount results.
- Mounting updates `mounts.toml` and refreshes `manifest.toml`.

### Phase 4: Materialization

- Add explicit commands:
  - `gxfs sync materialize docs/gotchas/openai-go`
  - `gxfs sync dematerialize docs/gotchas/openai-go`
  - `gxfs sync refresh docs/gotchas/openai-go`
- Materialized files are real markdown files under the local repo.
- Each materialized file should include or be paired with revision metadata.
- Decide whether materialized docs are committed, ignored, or controlled by a
  repo policy in `.gxfs/settings.toml` plus mount-specific policy in
  `.gxfs/mounts.toml`.

## Server Roadmap

### Phase 1: Multi-Repo Routing

- Replace the single adapter assumption with a registry:
  - repo name -> repo view
  - collection name -> shared collection
  - backend namespace -> store adapter
- Ensure request repo is carried through the store request and honored by
  Postgres queries.
- Define the cross-repo remote reference syntax before accepting non-self
  `repo` mounts.
- Keep the current store capability interfaces, but introduce a higher-level
  resolver before store operations.

### Phase 2: Search Service

- Add `/v1/search` for global or scoped discovery.
- Support filters:
  - repo
  - collection
  - language/library
  - doc type such as gotcha, how-to, API note, decision
  - tags
- Implement lexical search first with Postgres full-text search or trigram.
- Reserve the API shape for future embedding/vector search without requiring it
  in the first implementation.

### Phase 3: Performance

- Keep `ls/tree/find/stat` metadata-only.
- Avoid loading all content for large `grep` roots when an indexed backend can
  answer the query.
- Add pagination or result limits for large searches.
- Add ETag/revision support so clients can avoid re-fetching unchanged docs.
- Add observability:
  - request latency
  - cache hit/miss
  - search result count
  - backend query timings

## Database Redesign

Move from path-as-identity to document identity plus mount views.

### Proposed Tables

```sql
gxfs_docs(
  id uuid primary key,
  slug text not null,
  title text not null,
  kind text not null,
  content text not null,
  content_hash text not null,
  revision bigint not null,
  created_at timestamptz not null,
  updated_at timestamptz not null
)
```

```sql
gxfs_collections(
  id uuid primary key,
  name text not null unique,
  description text not null default '',
  visibility text not null,
  created_at timestamptz not null,
  updated_at timestamptz not null
)
```

```sql
gxfs_collection_docs(
  collection_id uuid not null references gxfs_collections(id),
  doc_id uuid not null references gxfs_docs(id),
  path text not null,
  primary key(collection_id, path),
  unique(collection_id, doc_id)
)
```

```sql
gxfs_repo_mounts(
  id uuid primary key,
  repo text not null,
  local_path text not null,
  remote_ref text not null,
  mode text not null,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  unique(repo, local_path)
)
```

```sql
gxfs_repo_docs(
  repo text not null,
  path text not null,
  doc_id uuid not null references gxfs_docs(id),
  mode text not null,
  primary key(repo, path)
)
```

Optional search index:

```sql
gxfs_doc_search(
  doc_id uuid primary key references gxfs_docs(id),
  search_vector tsvector not null
)
```

### Mapping Rules

- `gxfs_docs` owns content and revision.
- `gxfs_collections` groups reusable knowledge, such as
  `openai-go/v3/gotchas`.
- `gxfs_collection_docs.path` is the canonical path inside a collection.
- `gxfs_repo_mounts` records how a repo sees shared or repo-local docs.
- `gxfs_repo_docs` can be a denormalized resolved view for fast VFS operations.
- A single doc can appear at many local paths through different mounts.

## Auth and Permission Design

### Auth Modes

Start with simple modes:

- `none`: local/dev only.
- `bearer`: static token from `GXFS_TOKEN`.
- later: OIDC/JWT for team deployments.

### Permission Model

Permissions should be checked server-side, not only in the CLI.

Suggested capabilities:

- `repo:read`
- `repo:write`
- `repo:mount`
- `collection:read`
- `collection:write`
- `admin`

Mount mode should constrain operations:

- `readonly`: `ls/tree/find/stat/cat/grep/search`
- `writable`: read plus `write/edit/rm/sync push`
- `materialized`: local materialization allowed, but remote writes still depend
  on server permission

### Audit

Write operations should record:

- actor
- repo
- path
- doc id
- operation
- old revision
- new revision
- timestamp

This is especially important because agents may perform writes.

## Local Cache and Materialization

Use three cache levels:

1. Server metadata/content cache for backend performance.
2. CLI manifest cache for fast local tree/path discovery.
3. Optional materialized markdown files for native agent file tools.

Expiration should be revision-based where possible and TTL-based only as a
fallback. The CLI should prefer conditional fetches:

- If manifest revision matches server revision, reuse local metadata.
- If content hash matches, skip content download.
- If materialized file changed locally and remote also changed, report a
  conflict.

## Suggested Implementation Order

1. Fix `gxfs init` and config loading.
2. Add `.gxfs/mounts.toml` config and local-to-remote path resolver.
3. Fix cache TTL semantics and tree concurrency risks.
4. Decide the remote reference namespace for non-self repo refs and reusable
   collections.
5. Design and migrate the DB schema toward docs, collections, mounts, and
   revisions.
6. Implement multi-repo server registry.
7. Add `gxfs sync push` for existing repo docs.
8. Add lexical `gxfs search`.
9. Add `gxfs mount` and generated `.gxfs/manifest.toml`.
10. Add explicit materialization commands.
11. Add auth, permissions, and audit logging before broad write usage.

## Open Design Questions

- Should `.gxfs/manifest.toml` be committed by default, ignored by default, or
  controlled per repo?
- Should `gxfs search` search all public collections by default, or only
  collections allowed by the current server policy?
- Should shared docs be edited from consuming repos, or only from their owning
  collection namespace?
- If GXFS supports true cross-repo repo mounts, should the remote ref use query
  parameters, an explicit delimiter, or structured TOML fields?
- Should reusable library knowledge always become collections, even when the
  source happens to live in a Git repo?
- Should materialized docs include metadata in frontmatter, sidecar files, or
  only the manifest?
- Do we need first-class semantic search in the initial version, or is Postgres
  lexical search enough until the core mount model settles?
