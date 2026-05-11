# GXFS

GXFS gives agents Unix-like CLI commands for shared virtual filesystem content.
It is designed for internal docs and other project knowledge that should be
queried like a local `docs/` directory, while being served from a backend store.

The project has two binaries:

- `gxfs-server`: HTTP server that owns backend access.
- `gxfs`: thin CLI client used by agents and humans.

The CLI never connects to the database directly. It reads `.gxfs/settings.toml`,
talks to `gxfs-server`, and prints file-system-like output.

## Quick Start

Build the binaries:

```bash
go build ./cmd/gxfs
go build ./cmd/gxfs-server
```

Create a CLI config and agent instructions in the current project:

```bash
./gxfs init
```

By default this creates `.gxfs/settings.toml` and injects GXFS usage instructions
into `AGENTS.md`. To target Claude Code instead:

```bash
./gxfs init --agent claude
# or, for backwards compatibility:
./gxfs init --claude
```

To create only the config file without touching agent instruction files:

```bash
./gxfs init --no-instructions
```

Start the server with a server config:

```bash
GXFS_SERVER_CONFIG=conf/server.toml ./gxfs-server
```

Use the CLI with a project config:

```bash
GXFS_CONFIG=.gxfs/settings.toml ./gxfs tree /docs -L 3
GXFS_CONFIG=.gxfs/settings.toml ./gxfs grep "auth" /docs
GXFS_CONFIG=.gxfs/settings.toml ./gxfs cat /docs/README.md
```

If `GXFS_CONFIG` is not set, the CLI reads `.gxfs/settings.toml`. If
`GXFS_SERVER_CONFIG` is not set, the server reads `conf/server.toml`.

## CLI Config

Example `.gxfs/settings.toml`:

```toml
repo = "github.com/user/repo"

[server]
addr = "http://127.0.0.1:7635"

[mount]
include = ["/"]

[docs]
path = "/docs"
```

Fields:

- `repo`: logical repository name. This must match a repo configured on the
  server.
- `server.addr`: gxfs-server base URL.
- `mount.include`: visible path prefixes for the CLI. Defaults to `["/"]`.
- `docs.path`: default documentation root used in generated agent instructions.
  Defaults to `/docs`.

CLI config must not contain backend credentials.

## Server Config

Example `conf/server.toml` using PostgreSQL:

```toml
addr = "127.0.0.1:7635"

[[repos]]
name = "github.com/user/repo"

[repos.backend]
type = "postgres"

[repos.backend.postgres]
dsn = "${GXFS_POSTGRES_DSN}"
schema = "public"
nodes_table = "vfs_nodes"
content_table = "vfs_content"
repo_nodes_table = "vfs_repo_nodes"
cache_ttl = "30s"

[repos.backend.postgres.files]
path_column = "path"
kind_column = "kind"
size_column = "size"
mtime_column = "updated_at"
```

Notes:

- Environment variables in config files are expanded.
- This version supports exactly one configured repo per server process.
- PostgreSQL schema is auto-migrated on server startup. Missing GXFS tables are
  created with `CREATE TABLE IF NOT EXISTS`.
- `cache_ttl` is optional. If omitted, the Postgres adapter keeps its loaded tree
  until writes/deletes invalidate it or the process restarts.

## Common CLI Commands

List and inspect:

```bash
gxfs ls /docs
gxfs ls -la /docs
gxfs tree /docs -L 3
gxfs stat /docs/guide.md
gxfs stat -f /docs/guide.md
```

Read content:

```bash
gxfs cat /docs/guide.md
gxfs cat -n /docs/guide.md
gxfs cat -b /docs/guide.md
```

Search content:

```bash
gxfs grep "database" /docs
gxfs grep -i "database" /docs
gxfs grep -E "db|database" /docs
gxfs grep -C 2 "migration" /docs
gxfs grep --include "*.md" --exclude "archive/*" "token" /docs
gxfs grep -l "TODO" /docs
gxfs grep -c "TODO" /docs
```

Find paths:

```bash
gxfs find /docs --name "*.md"
gxfs find /docs --iname "*readme*"
gxfs find /docs --type f --maxdepth 3 --name "*.md"
gxfs find /docs --type d --name "api"
```

Write, edit, and delete:

```bash
gxfs write /docs/new.md "# New Doc"
cat local.md | gxfs write /docs/local.md
gxfs edit /docs/new.md --old "New" --new "Updated"
gxfs edit /docs/new.md --old "foo" --new "bar" --all
gxfs delete /docs/new.md
gxfs delete /docs/old-section
```

Sync local docs into GXFS:

```bash
gxfs sync push docs
gxfs sync push docs --manifest .gxfs/manifest.toml
```

## Command Reference

`gxfs ls [path]`

- `-l, --long`: long listing format.
- `-a, --all`: show hidden files.
- `-R, --recursive`: list recursively.
- `-d, --directory`: show the directory itself instead of its contents.
- `-t, --sort-time`: sort by modification time, newest first.
- `-S, --sort-size`: sort by size, largest first.
- `-r, --reverse`: reverse sort order.
- `-F, --classify` or `-p, --slash`: append directory indicators.

`gxfs tree [path]`

- `-L, --level`: maximum tree depth. Default is `2`.
- `-a, --all`: show hidden files.
- `-d, --dirs-only`: list directories only.
- `-f, --full-path`: print full path prefix.
- `-s, --size`: show file sizes.
- `-t, --sort-time`: sort by modification time.
- `--dirsfirst`: list directories before files.

`gxfs cat <path>`

- `-n, --number`: number all output lines.
- `-b, --number-nonblank`: number non-blank output lines.
- `-s, --squeeze-blank`: squeeze repeated blank lines.

`gxfs grep <pattern> [path]`

- `-E, --regex`: treat pattern as a regular expression.
- `-i, --ignore-case`: case-insensitive search.
- `-v, --invert-match`: show non-matching lines.
- `-w, --word-regexp`: match whole words.
- `-x, --line-regexp`: match whole lines.
- `-A, --after-context`: lines of trailing context.
- `-B, --before-context`: lines of leading context.
- `-C, --context`: lines of context before and after.
- `-l, --files-with-matches`: print only file names.
- `-c, --count`: print match counts per file.
- `-o, --only-matching`: print only matched text for literal searches.
- `--include`: only search matching file globs.
- `--exclude`: skip matching file globs.
- `-a, --all`: search hidden files.

`gxfs find [path]`

- `--name`: filename glob.
- `--iname`: case-insensitive filename glob.
- `-t, --type`: filter by type, `f` or `file` for files, `d` or `dir` for dirs.
- `--maxdepth`: maximum descent depth. `0` means unlimited.
- `--mindepth`: minimum descent depth.
- `-a, --all`: include hidden files.

`gxfs stat <path>`

- `-f, --terse`: single-line output.
- `-c, --format`: custom format string.

Format placeholders:

- `%n`: name
- `%p`: path
- `%k`: kind
- `%s`: size
- `%y`: modification time
- `%m`: metadata
- `%%`: literal percent sign

`gxfs write <path> [content]`

Creates or overwrites a file. Parent directories are created as needed. If
`content` is omitted, content is read from stdin.

`gxfs edit <path> --old <text> --new <text> [--all]`

Replaces text in a file. By default only the first occurrence is replaced.
Use `--all` to replace every occurrence.

`gxfs delete <path>`

Deletes a file or directory. Directory deletes are recursive.

`gxfs init [path]`

Creates `.gxfs/settings.toml` and, unless disabled, injects GXFS instructions
into an agent instruction file.

- default: write `AGENTS.md`
- `--agent claude`: write `CLAUDE.md`
- `--claude`: alias for `--agent claude`
- `--no-instructions`: write config only

The injected block is wrapped with:

```markdown
<!-- GXFS_START -->
...
<!-- GXFS_END -->
```

Running `gxfs init` again replaces the existing block instead of appending a
duplicate.

`gxfs sync push <local-path>`

Scans a local file or directory, uploads each file through the existing GXFS
write API, and updates `.gxfs/manifest.toml`.

- `--manifest`: custom manifest path. Defaults to `.gxfs/manifest.toml`.

Phase 2A/2B stores client-computed `sha256:<hex>` hashes, file size, and mtime
in the manifest. It does not change the server API or database schema.

## Agent Usage

For agent workflows, treat GXFS as the canonical way to browse shared project
docs. Prefer:

```bash
gxfs tree /docs -L 3
gxfs grep "topic" /docs
gxfs cat /docs/file.md
```

over scanning local files when the information is meant to come from shared
internal documentation. The default docs root is `/docs`, configurable through
`[docs].path` in `.gxfs/settings.toml`.

## Development

Run tests:

```bash
go test ./...
```

Run Postgres e2e tests:

```bash
go test -count=1 -tags=e2e ./e2e
```

Build:

```bash
go build ./cmd/gxfs
go build ./cmd/gxfs-server
```

Key packages:

- `cmd/gxfs`: Cobra CLI commands.
- `cmd/gxfs-server`: HTTP server entrypoint.
- `internal/client`: HTTP client that implements the store adapter interface.
- `internal/server`: HTTP API handler.
- `internal/store`: shared adapter interfaces and request/response types.
- `internal/store/postgres`: PostgreSQL adapter and embedded migrations.
- `internal/vfs`: in-memory tree semantics used by adapters.
