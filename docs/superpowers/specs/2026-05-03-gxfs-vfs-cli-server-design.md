# GXFS VFS CLI And Server Design

## Goal

Build `gxfs`, an agent-facing virtual filesystem CLI for Codex and Claude Code. Agents should inspect remote or structured knowledge with familiar commands such as `gxfs ls`, `gxfs tree`, `gxfs cat`, `gxfs grep`, and `gxfs find`, while a backend service handles storage-specific access and presents all mounted content as a filesystem.

## Non-Goals For The First Version

- No Claude Code `PreToolUse` command rewriting.
- No local daemon lifecycle manager.
- No write commands such as `cp`, `mv`, `rm`, or `edit`.
- No semantic/vector search command unless added after the Unix-like read path is stable.
- No direct CLI connection to Postgres.

## Architecture

GXFS uses a thin CLI and a backend service.

The CLI reads the current repo configuration, sends requests to `gxfs-server`, and renders compact terminal output for agents. It does not know how Postgres, SQLite, or future stores represent files.

The server owns storage adapters, mount filtering, file tree construction, cache refresh, logging, and command semantics. It loads configured repositories from storage backends, turns them into an in-memory tree, and serves `ls/tree/cat/grep/find/stat` over HTTP.

```text
Codex / Claude Code
        |
        v
 gxfs CLI  --repo config-->  gxfs-server HTTP API
        |                         |
        |                         v
        |                  store.Adapter
        |              /        |        \
        v             v         v         v
 compact output   postgres   sqlite   future stores
```

## Repository Configuration

Each codebase can contain a `gxfs.toml`. `GXFS_CONFIG` can point at another file when needed.

```toml
project = "gxfs"

[server]
addr = "http://127.0.0.1:7635"

[mount]
include = ["/"]
exclude = [
  "vendor/**",
  "node_modules/**",
  "java-reference/**"
]

[backend]
type = "postgres"

[backend.postgres]
dsn = "${GXFS_POSTGRES_DSN}"
schema = "public"
```

The repo config only needs `project`, `server.addr`, and mount preferences. Backend settings live in server config for production. Local development may point `gxfs-server --config` at a combined TOML file, but the CLI must not require backend credentials.

## CLI

The CLI binary is `gxfs`.

Commands:

- `gxfs ls [path]`
- `gxfs tree [path] -L <depth>`
- `gxfs cat <path>`
- `gxfs grep <pattern> [path]`
- `gxfs find [path] -name <glob>`
- `gxfs stat <path>`
- `gxfs config doctor`

CLI requirements:

- Use Cobra so `gxfs --help` and `gxfs <command> --help` are good enough for agents to self-learn.
- Default path is `/` for commands where that makes sense.
- Output should be Unix-like and compact by default.
- Errors should name the path, repo, and failed operation.
- `--json` can be global for machine-readable output, but text remains the default for agents.
- `grep` uses plain substring matching by default. `gxfs grep -E <regex> <path>` enables regular expressions.

Example output:

```bash
$ gxfs ls /docs
api.md
architecture.md
go/

$ gxfs grep "type Adapter" /go
/go/internal/store/store.go:41:type Adapter interface {
```

## Agent Instruction Document

Create `docs/agents/gxfs.md`, designed to be injected into `AGENTS.md` or `CLAUDE.md`.

Content should tell agents:

- Prefer `gxfs` for virtual filesystem inspection.
- Use `gxfs --help` and per-command help when unsure.
- Start with `gxfs tree / -L 2` or `gxfs ls /`.
- Use `gxfs cat` for exact file content.
- Use `gxfs grep` and `gxfs find` before broad reading.
- Fall back to direct database access only when GXFS cannot answer.

## HTTP API

The CLI talks to the backend service over HTTP.

Initial endpoints:

- `GET /v1/repos/{repo}/ls?path=/...`
- `GET /v1/repos/{repo}/tree?path=/...&depth=2`
- `GET /v1/repos/{repo}/cat?path=/...`
- `GET /v1/repos/{repo}/grep?path=/...&pattern=...`
- `GET /v1/repos/{repo}/find?path=/...&name=*.go`
- `GET /v1/repos/{repo}/stat?path=/...`
- `GET /healthz`

Request path values are VFS paths, not local filesystem paths.

## Backend Service

Use `go-zero` rather than bare `net/http`. It provides service structure, config loading, middleware, logging, and operational conventions without making the project feel like Java.

The server is responsible for:

- Loading server config.
- Opening storage adapters.
- Building or refreshing repository file trees.
- Applying include/exclude mount filters.
- Serving command APIs.
- Logging command, repo, path, latency, and backend errors.
- Returning stable JSON response shapes to the CLI.

## Store Capability Interfaces

The storage boundary is intentionally split by command capability. Each interface maps directly to a command an agent understands.

```go
type Lister interface {
	LS(ctx context.Context, req LSRequest) (*LSResponse, error)
}

type Treer interface {
	Tree(ctx context.Context, req TreeRequest) (*TreeResponse, error)
}

type Catter interface {
	Cat(ctx context.Context, req CatRequest) (*CatResponse, error)
}

type Grepper interface {
	Grep(ctx context.Context, req GrepRequest) (*GrepResponse, error)
}

type Finder interface {
	Find(ctx context.Context, req FindRequest) (*FindResponse, error)
}

type Statter interface {
	Stat(ctx context.Context, req StatRequest) (*StatResponse, error)
}

type Adapter interface {
	Lister
	Treer
	Catter
	Grepper
	Finder
	Statter
}
```

Naming rationale:

- Command layer uses `ls/tree/cat/grep/find`.
- Store capability interfaces use `Lister/Treer/Catter/Grepper/Finder`.
- Method names stay visually close to commands: `LS`, `Tree`, `Cat`, `Grep`, `Find`.
- The full backend is `store.Adapter`, composed from the capability interfaces.

Implementation rule:

```go
var _ store.Adapter = (*Adapter)(nil)
```

Every backend implementation must include this assertion.

## VFS Model

The server keeps a repository tree in memory.

Core model:

- `Node`: path, name, type, size, mod time, metadata.
- `Tree`: path-to-node map plus parent-to-children index.
- `Match`: grep result with path, line number, and line text.

Tree operations:

- `LS` reads immediate children from the parent-to-children index.
- `Tree` walks the in-memory tree to a requested depth.
- `Find` walks the in-memory tree and applies glob matching to names.
- `Stat` reads the node map.
- `Cat` fetches file content from the adapter and returns exact content.
- `Grep` can use backend-native text search first, then confirm matches line-by-line before returning.

The in-memory tree shape can borrow the practical idea from `../vkfs`: load a path tree once, build indexes, and keep navigation operations cheap. GXFS should not copy VKFS's vector-store assumptions.

## Postgres Backend

The first storage backend is Postgres.

Package layout:

```text
internal/store/store.go
internal/store/postgres/adapter.go
internal/store/postgres/query.go
internal/vfs/tree.go
internal/vfs/path.go
```

Use `pgxpool` for connections.

The initial Postgres adapter should support two practical modes:

1. File-table mode, where rows already represent files with path and content columns.
2. Query mode, where config provides SQL for listing files and reading content.

This keeps GXFS useful against real existing databases instead of forcing one schema immediately.

Suggested config:

```toml
[backend.postgres.files]
table = "vfs_files"
path_column = "path"
content_column = "content"
size_column = "size"
mtime_column = "updated_at"
```

The Postgres adapter should build the tree from paths, synthesize missing parent directories, and apply mount filters before exposing results.

## Go Style Rules

- Define interfaces only at real polymorphic boundaries.
- Use concrete structs and package functions elsewhere.
- Keep files small and responsibility-focused.
- Avoid Java-style interface/implementation pairs when one concrete type exists.
- Prefer simple constructors returning concrete types.
- Use `context.Context` on all request paths.
- Use wrapped errors with enough operation/path/repo context.
- Use table-driven tests for VFS behavior and adapter request translation.
- Add compile-time interface assertions for every store adapter.

## Testing Strategy

Tests should be written before implementation.

Core tests:

- Config loading expands environment variables and respects default paths.
- VFS tree builder synthesizes parent directories.
- Mount include/exclude rules hide excluded paths.
- `LS` returns sorted immediate children.
- `Tree` respects depth.
- `Find` matches glob names under a root.
- `Cat` returns exact file content.
- `Grep` returns path, line number, and matching line.
- CLI commands render compact text output from fake server responses.
- Postgres adapter satisfies `store.Adapter`.

Integration tests:

- Start Postgres with test data.
- Start `gxfs-server`.
- Run `gxfs ls`, `gxfs cat`, `gxfs grep`, `gxfs find`, and `gxfs tree` against it.

## First-Version Decisions

- `gxfs-server` uses `go-zero`.
- Production backend credentials live in server config, not agent-facing repo config.
- `grep` defaults to plain substring matching. `-E` enables regex.
- The CLI never connects directly to Postgres.
