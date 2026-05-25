# Development and Server Operations

This document covers repository development and administration of the GXFS
backend. End-user CLI capabilities belong in the top-level `README.md`.

## Server Runtime

`gxfs-server` owns backend access and exposes the HTTP API used by the thin
`gxfs` client. Install it near the configured PostgreSQL store:

```bash
go install github.com/austiecodes/gxfs/cmd/gxfs-server@latest
```

From a local checkout:

```bash
go install ./cmd/gxfs-server
```

Start the service with a server-owned configuration:

```bash
GXFS_SERVER_CONFIG=/etc/gxfs/server.toml gxfs-server
```

If `GXFS_SERVER_CONFIG` is unset, the binary reads `conf/server.toml`.

## Server Configuration

Example `/etc/gxfs/server.toml` using PostgreSQL document storage:

```toml
addr = "127.0.0.1:7635"

[[repos]]
name = "github.com/user/repo"
writable = true

[repos.backend]
type = "doc_postgres"

[repos.backend.postgres]
dsn = "${GXFS_POSTGRES_DSN}"
schema = "public"
cache_ttl = "30s"
```

- Environment variables in config files are expanded.
- A server can configure multiple repositories. HTTP requests route by
  `/v1/repos/{repo}/...`.
- `doc_postgres` is the document-centric backend required by collections and
  orphan-content GC.
- PostgreSQL schema migration runs during server startup.
- `writable = true` permits cross-repository writable mounts to target that
  repository. Otherwise cross-repository writes are rejected.
- `cache_ttl` is optional. Without it, repository trees remain cached until
  mutation invalidation or process restart.

## Orphan Content GC

GC is an administrative database maintenance action, not a request sent by
`gxfs` to a running HTTP server. The `gxfs-server` executable also contains an
out-of-band maintenance subcommand: each invocation loads the server config,
connects directly to the configured `doc_postgres` database, and removes
document rows that are no longer referenced by repository paths or
collections.

It defaults to dry-run mode and uses a grace period to avoid deleting recently
created content:

```bash
GXFS_SERVER_CONFIG=/etc/gxfs/server.toml gxfs-server gc --dry-run
GXFS_SERVER_CONFIG=/etc/gxfs/server.toml gxfs-server gc --force --grace-hours 24
```

The current executable form is `gxfs-server gc`, although generated help
incorrectly presents `gxfs-server gc run`. See the
[known CLI help mismatch](../gotchas/cli/gxfs-server-gc-run-usage.md).

## Build and Test

```bash
go test ./...
go test ./internal/store
go test ./internal/vfs -run TestGrep
go test -count=1 -tags=e2e ./test/e2e
go build ./cmd/gxfs
go build ./cmd/gxfs-server
```

## Package Layout

- `cmd/gxfs`: Cobra CLI commands.
- `cmd/gxfs-server`: HTTP server and out-of-band administrative command
  entrypoint.
- `internal/client`: HTTP client implementing the store adapter interface.
- `internal/server`: HTTP API handler.
- `internal/store`: adapter interfaces and request/response types.
- `internal/store/postgres`: PostgreSQL adapters, migrations, collections, and
  orphan-content GC implementation.
- `internal/mount`: local-to-remote mount resolution and composed views.
- `internal/syncmanifest`: manifest persistence and materialization tracking.
- `internal/vfs`: in-memory filesystem tree semantics used by adapters.
