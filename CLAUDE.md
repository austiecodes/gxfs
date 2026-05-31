# AGENTS

## Document Index

- `docs/dev/README.md` — developer and operator guide: server configuration, maintenance commands, build/test workflow, and package layout
- `docs/gotchas/` — pitfall notes. Create subdirectories by topic (for example `pg/`, `testing/`, or `go-zero/`). One Markdown file per pitfall, using the format: problem -> cause -> solution

## Documentation Update Rules

- Add or update a file under `docs/gotchas/` whenever you hit a non-obvious bug, tooling issue, integration trap, flaky behavior, or debugging lesson that is likely to waste time again if left undocumented.
- Treat `docs/gotchas/` as a required follow-up for real pitfalls encountered during implementation or testing, not as optional extra documentation.

## PostgreSQL (Docker)

```bash
docker exec -it gxfs-pg psql -U gxfs -d gxfs                  # connect
docker start gxfs-pg                                          # start
docker stop gxfs-pg                                           # stop
docker exec gxfs-pg psql -U gxfs -d gxfs -c "SELECT count(*) FROM vfs_files"
```

DSN: `postgres://gxfs:gxfs@localhost:5432/gxfs?sslmode=disable`

## Build & Test

```bash
go test ./...                              # run all tests
go test ./internal/store                   # run a single package
go test ./internal/vfs -run TestGrep       # run a single test
go build ./cmd/gxfs                        # build the CLI
go build ./cmd/gxfs-server                 # build the server
```

## Architecture Overview

GXFS is an agent-oriented virtual file system built as a thin CLI plus a backend server.

```text
gxfs CLI  ──HTTP──>  gxfs-server  ──>  store.Adapter
                                          ├─ memory   (testing/development)
                                          └─ postgres (production, pgxpool)
```

- **CLI** (`cmd/gxfs`) — Cobra-based command line app. Reads `.gxfs/settings.toml`, talks to the server through the HTTP client, and must not know about storage internals.
- **Server** (`cmd/gxfs-server`) — go-zero HTTP service. Loads `conf/server.toml`, owns the store adapter, and exposes `/v1/repos/{op}?repo=...` APIs.
- **Store boundary** — `internal/store/store.go` defines the capability interfaces (`Lister`, `Treer`, `Catter`, `Grepper`, `Finder`, `Statter`, `Writer`) and combines them into `store.Adapter`. Every adapter must include a compile-time assertion: `var _ store.Adapter = (*Adapter)(nil)`.
- **VFS tree** (`internal/vfs/tree.go`) — in-memory tree that auto-synthesizes parent directories and provides `ls`, `tree`, `cat`, `grep`, `find`, and `stat`.
- **Client** (`internal/client/client.go`) — HTTP client that implements `store.Adapter`, with URLs shaped as `/v1/repos/{op}?repo=...&path=...`.
- **Config** (`internal/config/config.go`) — TOML config. CLI config must not contain backend credentials. Server config owns storage connection details. Environment variables are expanded automatically.

## Key Conventions

- Define interfaces only at real polymorphic boundaries (`store.Adapter`). Use concrete structs elsewhere.
- The Postgres adapter builds the VFS tree lazily: load metadata (`vfs_nodes`) once, then load and cache content (`vfs_content`) on demand.
- Database schema: `vfs_nodes` (path PK) + `vfs_content` (path PK) + `vfs_repo_nodes` (repo, path) for many-to-many repo mapping. A document is stored once and shared across repos.
- `grep` uses plain-text substring matching by default. `-E` enables regex mode.
- The CLI must never connect to the database directly. All operations go through the server HTTP API.
