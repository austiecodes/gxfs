# GXFS

GXFS gives agents Unix-like CLI commands for shared virtual filesystem content.
It is designed for internal docs and other project knowledge that should be
queried like a local `docs/` directory, while being served from a backend store.

The project has two binaries:

- `gxfs-server`: HTTP server that owns backend access.
- `gxfs`: thin CLI client used by agents and humans.

The CLI never connects to the database directly. It reads `.gxfs/settings.toml`,
talks to `gxfs-server`, and prints file-system-like output.

## Features

| Area | What GXFS provides | Primary commands |
| --- | --- | --- |
| Virtual filesystem browsing | Unix-like listing, tree rendering, reads, metadata inspection, substring or regex grep, and path finding over mounted documentation. | `ls`, `tree`, `cat`, `stat`, `grep`, `find` |
| Document discovery | Ranked full-text search, lexical lookup returning `repo://` references, glob discovery, and repository enumeration. Discovery can operate outside the mounted local view. | `search`, `locate`, `glob`, `repo ls` |
| Shared docs mounts | A project can compose documentation from repository namespaces or reusable docs namespaces into local paths, with read-only or writable mount policy and direct remote preview. | `mount`, `mount sources`, `mount attach`, `cat repo://...` |
| Writing and synchronization | Create, replace, or delete remote docs; push local docs; pull remote metadata or files; track hashes and detect conflicting local/remote changes. | `write`, `edit`, `rm`, `sync refresh`, `sync materialize`, `sync dematerialize` |
| Curated docsets | Optional advanced workflow for curated cross-repository document sets when the server enables docsets. Shared docs should usually use `docs://` namespaces and mounts instead. | `docset` |
| Agent integration and observability | Generate agent instructions, install Codex or Claude hooks, refresh docs at session start, record CLI audit JSONL, and persist hook-correlated usage events server-side when enabled by hooks. | `init`, `hook session-start` |

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

Then initialize a project config, agent instructions, and the server-side repo
registration:

```bash
gxfs init --register --repo github.com/user/repo
```

By default this creates `.gxfs/settings.toml` and `.gxfs/mounts.toml`, then
injects a minimal GXFS entry into `AGENTS.md` and writes the local GXFS skill at
`.gxfs/skills/gxfs/SKILL.md`. The agent instruction file stays small; the skill
indexes detailed GXFS workflows and loads scenario references only when needed.
Use `--server` if the server is not at `http://127.0.0.1:7635`. To target
Claude Code instead:

```bash
gxfs init --agent claude --register --repo github.com/user/repo
```

To create only the configuration files without touching agent instruction
files:

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

Hook-correlated CLI calls write local audit JSONL and also try to report a
structured usage event to `gxfs-server` with a short timeout. Reporting failures
do not change the original command exit code. Set `GXFS_USAGE_REPORT=0` to
disable server reporting, or `GXFS_USAGE_REPORT=1` to report non-hooked CLI
calls as well.

Use the CLI with a project config:

```bash
gxfs tree /docs -L 3
gxfs grep "auth" /docs
gxfs cat /docs/README.md
```

If `GXFS_CONFIG` is not set, the CLI reads `.gxfs/settings.toml`.

## CLI Config

Example `.gxfs/settings.toml`:

```toml
repo = "github.com/user/repo"

[server]
addr = "http://127.0.0.1:7635"

[docs]
path = "docs"
```

Fields:

- `repo`: logical repository name. This must match a repo registered on the
  server.
- `server.addr`: gxfs-server base URL.
- `docs.path`: default documentation root used in generated agent instructions.
  It is also used for the default self mount when no mount file exists.
  Defaults to `docs`.

CLI config must not contain backend credentials.

Mounted paths are configured separately in `.gxfs/mounts.toml`:

```toml
version = 1

[[mounts]]
local = "docs"
remote = "repo://self/docs"
mode = "writable"
source = "default"
```

Each mount maps a local CLI path to a source path. Repositories use
`repo://<repo>/<path>` references. Reusable documentation trees use
`docs://<name>/<path>` references. Mounts default to `readonly` when created
with `gxfs mount add`.

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

Discover content, including unmounted repositories:

```bash
gxfs search "migration rollback" --path /docs
gxfs locate "openai client" --all-repos
gxfs glob "**/*.md" --all-repos
gxfs repo ls
gxfs cat repo://shared-docs/docs/guide.md
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
gxfs rm /docs/new.md
gxfs rm /docs/old-section
```

Compose a local view from repositories or reusable docs namespaces:

```bash
gxfs mount sources
gxfs mount add repo://shared-docs/docs docs/shared
gxfs mount add docs://openai-go-sdk/reference docs/openai-go-sdk
gxfs mount add repo://shared-docs/docs docs/shared --mode writable
gxfs mount ls
gxfs mount attach openai-go --into docs/libs/openai-go
gxfs mount rm docs/shared
```

Sync local docs into GXFS:

```bash
gxfs sync push docs
gxfs sync pull docs
gxfs sync pull docs --materialize
gxfs sync refresh docs
gxfs sync materialize docs
gxfs sync dematerialize docs
gxfs sync push docs --manifest .gxfs/manifest.toml
```

Curated docsets are optional and advanced. Prefer `docs://` namespaces for
reusable documentation trees. Use docsets only when the server explicitly
enables curated document sets:

```bash
gxfs docset create best-practices --description "Reusable guidance"
gxfs docset add best-practices /go/errors.md --source repo://shared-docs/go/errors.md
gxfs docset show best-practices
gxfs cat docset://best-practices/go/errors.md
gxfs mount add docset://best-practices docs/best-practices
gxfs docset rm best-practices /go/errors.md
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

`gxfs rm <path>`

Deletes a file or directory. Directory deletes are recursive.

`gxfs search <query>`

Runs ranked full-text search in the current repository and prints matching
snippets. Unlike mounted browsing commands, search is repository discovery.

- `--path`: limit results to a path prefix.
- `--limit`, `--offset`: paginate results.
- `--json`: emit structured output.

`gxfs locate <query>`

Performs ranked lexical lookup and emits `repo://` references suitable for
`gxfs cat` or mounting.

- `--all-repos`: query every repository registered on the server.
- `--limit`: cap returned results.
- `--json`: emit structured output.

`gxfs glob <pattern>`

Discovers document paths by glob, including `**` recursive matching.

- `--all-repos`: query every repository registered on the server.
- `--limit`, `--offset`: paginate results.
- `--long`: include size and modification time.

`gxfs repo ls`

Lists repositories available from the configured server.

`gxfs mount add <remote-ref> <local-path>`

Adds a mapping in `.gxfs/mounts.toml` from a local path to
`repo://self/<path>` or `repo://<repo>/<path>` and refreshes the local
manifest unless disabled.

- `--mode readonly|writable`: controls whether local writes may flow through
  the mount. Defaults to `readonly`.
- `--force`: replace a mount at the same local path.
- `--no-refresh`: skip the post-add manifest refresh.

Use `gxfs mount ls` to inspect mappings and `gxfs mount rm <local-path>`
to remove a mapping after any materialized files beneath it have been
dematerialized.

`gxfs mount attach <keyword-or-repo> --into <local-path>`

Finds a uniquely matching repository name and adds a read-only root mount.

- `--dry-run`: preview the resolved mount.
- `--force`: replace a mount already using the target local path.

`gxfs init [path]`

Creates `.gxfs/settings.toml` and `.gxfs/mounts.toml` and, unless disabled,
injects a minimal GXFS entry into an agent instruction file plus the local GXFS
skill.

- default: write `AGENTS.md` and `.gxfs/skills/gxfs/SKILL.md`
- `--repo <name>`: set the logical repository name
- `--server <url>`: set the `gxfs-server` base URL
- `--mode md|skill|md,skill`: choose Markdown instructions, local skill output,
  or both; default is `md,skill`
- `--agent claude`: write `CLAUDE.md`
- `--register`: register the repo with `gxfs-server`
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

`gxfs sync refresh <path>`

Refreshes `.gxfs/manifest.toml` for remote docs under a path without writing
local files.

- `--manifest`: custom manifest path. Defaults to `.gxfs/manifest.toml`.

`gxfs sync materialize <path>`

Refreshes the manifest and writes remote docs under the path to local markdown
files.

- `--manifest`: custom manifest path. Defaults to `.gxfs/manifest.toml`.

`gxfs sync dematerialize <path>`

Marks manifest entries under the path as remote-only and removes local
materialized files.

- `--manifest`: custom manifest path. Defaults to `.gxfs/manifest.toml`.
- `--keep-files`: update the manifest but leave local files in place.

`gxfs docset <subcommand>`

Manages optional curated cross-repository docsets when enabled by the
configured server. Prefer `docs://` namespaces plus `gxfs mount add` for
reusable documentation trees. Docset names accept lowercase letters,
digits, `-`, and `_`.

- `create <name> [--description <text>]`: create a docset.
- `list [--json]`: list docsets.
- `show <name> [--json]`: show members and their `docset://` references.
- `add <name> <docset-path> --source repo://<repo>/<path>`: add a source
  document at a stable docset path.
- `rm <name> <docset-path>`: remove a member.

Use `gxfs cat docset://<name>/<path>` to read a docset member. Use
`gxfs mount add docset://<name> <local-path>` to mount a read-only view of the
member tree. Change membership with `gxfs docset add` and `gxfs docset rm`;
use `docs://...` namespaces for writable reusable documentation trees.

`gxfs config doctor`

Prints the configured repository selected for CLI requests.

`gxfs hook session-start`

Refreshes manifest metadata and changed materialized files for agent session
startup. Installed Codex and Claude hooks call this operation with a timeout
so it does not block the session.

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
