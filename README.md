# GXFS

GXFS gives agents Unix-like CLI commands for shared virtual filesystem content.
It is designed for internal docs and other project knowledge that should be
queried like a local `docs/` directory, while being served from a backend store.

The project has two binaries:

- `gxfs-server`: HTTP server that owns backend access.
- `gxfs`: thin CLI client used by agents and humans.

The CLI never connects to the database directly. It reads `.gxfs/settings.toml`,
talks to `gxfs-server`, and prints file-system-like output.

## Install the CLI

The `gxfs` CLI is the thin client used by humans and agents. It does not connect
to PostgreSQL directly; it reads `.gxfs/settings.toml` and talks to a
`gxfs-server` HTTP endpoint.

Install the CLI from GitHub:

```bash
go install github.com/austiecodes/gxfs/cmd/gxfs@latest
```

Or, from a local checkout:

```bash
go install ./cmd/gxfs
```

Make sure Go's bin directory is on your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Then initialize a project config and agent instructions:

```bash
gxfs init
```

By default this creates `.gxfs/settings.toml` and injects GXFS usage instructions
into `AGENTS.md`. To target Claude Code instead:

```bash
gxfs init --agent claude
```

To create only the config file without touching agent instruction files:

```bash
gxfs init --no-instructions
```

Install agent hooks when you want GXFS audit correlation to be injected
automatically into agent-driven CLI calls:

```bash
# User-level hooks are the default.
gxfs init --hook codex
gxfs init --hook claude

# Project-level hooks live in the current repo.
gxfs init --hook codex --scope project
gxfs init --hook claude --scope project
```

Codex requires reviewing newly installed hooks before they run. After installing
Codex hooks, open Codex and use `/hooks` to trust them.

Use the CLI with a project config:

```bash
gxfs tree /docs -L 3
gxfs grep "auth" /docs
gxfs cat /docs/README.md
```

If `GXFS_CONFIG` is not set, the CLI reads `.gxfs/settings.toml`.

## Deploy the Server

The `gxfs-server` binary owns backend access and should run near the configured
store. Install it separately from the CLI:

```bash
go install github.com/austiecodes/gxfs/cmd/gxfs-server@latest
```

Or, from a local checkout:

```bash
go install ./cmd/gxfs-server
```

Create a server config and start the server:

```bash
GXFS_SERVER_CONFIG=/etc/gxfs/server.toml gxfs-server
```

If `GXFS_SERVER_CONFIG` is not set, the server reads `conf/server.toml`.

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

Notes:

- Environment variables in config files are expanded.
- A server can configure multiple repos. Requests route by the
  `/v1/repos/{repo}/...` path segment to the matching repo backend.
- `doc_postgres` is the current document-centric backend used by collections,
  locate, cross-repo refs, and GC.
- PostgreSQL schema is auto-migrated on server startup. Missing GXFS tables and
  indexes are created with `CREATE TABLE IF NOT EXISTS`.
- `writable = true` allows cross-repo writable mounts to write through to that
  repo. Omit it or set it to `false` for read-only repos.
- `cache_ttl` is optional. If omitted, the Postgres adapter keeps each repo's
  loaded tree until writes/deletes invalidate it or the process restarts.

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
gxfs sync pull docs
gxfs sync pull docs --materialize
gxfs refresh docs
gxfs materialize docs
gxfs dematerialize docs
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
- `--hook codex`: install user-level Codex hooks in `~/.codex/`
- `--hook claude`: install user-level Claude Code hooks in `~/.claude/`
- `--hook codex --scope project`: install project Codex hooks in `.codex/`
- `--hook claude --scope project`: install project Claude Code hooks in
  `.claude/`

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

`gxfs sync pull <local-path>`

Reads remote GXFS docs under the given path and updates `.gxfs/manifest.toml`.
By default it only refreshes the manifest; it does not write local files.

- `--manifest`: custom manifest path. Defaults to `.gxfs/manifest.toml`.
- `--materialize`: write pulled docs to local files.
- `--force-local`: resolve conflicts by pushing local content back to GXFS.
- `--force-remote`: resolve conflicts by accepting remote content locally.

Conflict detection compares the manifest's last synced hash with current local
and remote hashes. If both changed, pull fails unless one force flag is used.

`gxfs refresh <path>`

Refreshes `.gxfs/manifest.toml` for remote docs under a path without writing
local files.

- `--manifest`: custom manifest path. Defaults to `.gxfs/manifest.toml`.

`gxfs materialize <path>`

Refreshes the manifest and writes remote docs under the path to local markdown
files.

- `--manifest`: custom manifest path. Defaults to `.gxfs/manifest.toml`.

`gxfs dematerialize <path>`

Marks manifest entries under the path as remote-only and removes local
materialized files.

- `--manifest`: custom manifest path. Defaults to `.gxfs/manifest.toml`.
- `--keep-files`: update the manifest but leave local files in place.

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
