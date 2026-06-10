# ROLIO

ROLIO gives agents Unix-like CLI commands for shared virtual filesystem content.
It is designed for internal docs and other project knowledge that should be
queried like a local `docs/` directory, while being served from a backend store.

The project has two binaries:

- `rolio-server`: HTTP server that owns backend access.
- `rolio`: thin CLI client used by agents and humans.

The CLI never connects to the database directly. It reads `.rolio/settings.toml`,
talks to `rolio-server`, and prints file-system-like output.

## Features

| Area | What ROLIO provides | Primary commands |
| --- | --- | --- |
| Virtual filesystem browsing | Unix-like listing, tree rendering, reads, metadata inspection, substring or regex grep, and path finding over mounted documentation. | `ls`, `tree`, `cat`, `stat`, `grep`, `find` |
| Document discovery | Ranked full-text search, lexical lookup returning `repo://` references, glob discovery, and repository enumeration. Discovery can operate outside the mounted local view. | `search`, `locate`, `glob`, `repo ls` |
| Shared docs mounts | A project can compose documentation from repository namespaces or reusable docs namespaces into local paths, with read-only or writable mount policy and direct remote preview. | `mount`, `mount sources`, `mount attach`, `cat repo://...` |
| Writing and synchronization | Create, replace, or delete remote docs; push local docs; pull remote metadata or files; track hashes and detect conflicting local/remote changes. | `write`, `edit`, `rm`, `sync refresh`, `sync materialize`, `sync dematerialize` |
| Curated docsets | Optional advanced workflow for curated cross-repository document sets when the server enables docsets. Shared docs should usually use `docs://` namespaces and mounts instead. | `docset` |
| Agent integration and observability | Generate agent instructions, install Codex or Claude hooks, refresh docs at session start, record CLI audit JSONL, and persist hook-correlated usage events server-side when enabled by hooks. | `init`, `hook session-start` |

## Install the CLI

The `rolio` CLI is the thin client used by humans and agents. It does not connect
to PostgreSQL directly; it reads `.rolio/settings.toml` and talks to a
`rolio-server` HTTP endpoint.

Install the CLI from GitHub:

```bash
go install github.com/austiecodes/rolio/cmd/rolio@latest
```

Or, from a local checkout:

```bash
go install ./cmd/rolio
```

Make sure Go's bin directory is on your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Then initialize a project config, agent instructions, and the server-side repo
registration:

```bash
rolio init --register --repo github.com/user/repo
```

By default this creates `.rolio/settings.toml` and `.rolio/mounts.toml`, injects a
minimal ROLIO entry into `AGENTS.md`, installs the ROLIO skill to
`~/.claude/skills/rolio-skill/SKILL.md` and `~/.codex/skills/rolio-skill/SKILL.md`, and sets up
hooks in both user config dirs. The agent instruction file stays small; the skill
indexes detailed ROLIO workflows and loads scenario references only when needed.
Use `--server` if the server is not at `http://127.0.0.1:7635`. To target
Claude Code instead:

```bash
rolio init --agent claude --register --repo github.com/user/repo
```

To create only the configuration files without touching agent instruction
files:

```bash
rolio init --no-instructions
```

Install agent hooks when you want ROLIO audit correlation to be injected
automatically into agent-driven CLI calls:

```bash
# User-level hooks are the default.
rolio init --hook codex
rolio init --hook claude

# Project-level hooks live in the current repo.
rolio init --hook codex --scope project
rolio init --hook claude --scope project
```

Codex requires reviewing newly installed hooks before they run. After installing
Codex hooks, open Codex and use `/hooks` to trust them.

Hook-correlated CLI calls write local audit JSONL and also try to report a
structured usage event to `rolio-server` with a short timeout. Reporting failures
do not change the original command exit code. Set `ROLIO_USAGE_REPORT=0` to
disable server reporting, or `ROLIO_USAGE_REPORT=1` to report non-hooked CLI
calls as well.

Use the CLI with a project config:

```bash
rolio tree /docs -L 3
rolio grep "auth" /docs
rolio cat /docs/README.md
```

If `ROLIO_CONFIG` is not set, the CLI reads `.rolio/settings.toml`.

## CLI Config

Example `.rolio/settings.toml`:

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
- `server.addr`: rolio-server base URL.
- `docs.path`: default documentation root used in generated agent instructions.
  It is also used for the default self mount when no mount file exists.
  Defaults to `docs`.

CLI config must not contain backend credentials.

Mounted paths are configured separately in `.rolio/mounts.toml`:

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
with `rolio mount add`.

## Common CLI Commands

List and inspect:

```bash
rolio ls /docs
rolio ls -la /docs
rolio tree /docs -L 3
rolio stat /docs/guide.md
rolio stat -f /docs/guide.md
```

Read content:

```bash
rolio cat /docs/guide.md
rolio cat -n /docs/guide.md
rolio cat -b /docs/guide.md
```

Discover content, including unmounted repositories:

```bash
rolio search "migration rollback" --path /docs
rolio locate "openai client" --all-repos
rolio glob "**/*.md" --all-repos
rolio repo ls
rolio cat repo://shared-docs/docs/guide.md
```

Search content:

```bash
rolio grep "database" /docs
rolio grep -i "database" /docs
rolio grep -E "db|database" /docs
rolio grep -C 2 "migration" /docs
rolio grep --include "*.md" --exclude "archive/*" "token" /docs
rolio grep -l "TODO" /docs
rolio grep -c "TODO" /docs
```

Find paths:

```bash
rolio find /docs --name "*.md"
rolio find /docs --iname "*readme*"
rolio find /docs --type f --maxdepth 3 --name "*.md"
rolio find /docs --type d --name "api"
```

Write, edit, and delete:

```bash
rolio write /docs/new.md "# New Doc"
cat local.md | rolio write /docs/local.md
rolio edit /docs/new.md --old "New" --new "Updated"
rolio edit /docs/new.md --old "foo" --new "bar" --all
rolio rm /docs/new.md
rolio rm /docs/old-section
```

Compose a local view from repositories or reusable docs namespaces:

```bash
rolio mount sources
rolio mount add repo://shared-docs/docs docs/shared
rolio mount add docs://openai-go-sdk/reference docs/openai-go-sdk
rolio mount add repo://shared-docs/docs docs/shared --mode writable
rolio mount ls
rolio mount attach openai-go --into docs/libs/openai-go
rolio mount rm docs/shared
```

Sync local docs into ROLIO:

```bash
rolio sync push docs
rolio sync pull docs
rolio sync pull docs --materialize
rolio sync refresh docs
rolio sync materialize docs
rolio sync dematerialize docs
rolio sync push docs --manifest .rolio/manifest.toml
```

Curated docsets are optional and advanced. Prefer `docs://` namespaces for
reusable documentation trees. Use docsets only when the server explicitly
enables curated document sets:

```bash
rolio docset create best-practices --description "Reusable guidance"
rolio docset add best-practices /go/errors.md --source repo://shared-docs/go/errors.md
rolio docset show best-practices
rolio cat docset://best-practices/go/errors.md
rolio mount add docset://best-practices docs/best-practices
rolio docset rm best-practices /go/errors.md
```

## Command Reference

`rolio ls [path]`

- `-l, --long`: long listing format.
- `-a, --all`: show hidden files.
- `-R, --recursive`: list recursively.
- `-d, --directory`: show the directory itself instead of its contents.
- `-t, --sort-time`: sort by modification time, newest first.
- `-S, --sort-size`: sort by size, largest first.
- `-r, --reverse`: reverse sort order.
- `-F, --classify` or `-p, --slash`: append directory indicators.

`rolio tree [path]`

- `-L, --level`: maximum tree depth. Default is `2`.
- `-a, --all`: show hidden files.
- `-d, --dirs-only`: list directories only.
- `-f, --full-path`: print full path prefix.
- `-s, --size`: show file sizes.
- `-t, --sort-time`: sort by modification time.
- `--dirsfirst`: list directories before files.

`rolio cat <path>`

- `-n, --number`: number all output lines.
- `-b, --number-nonblank`: number non-blank output lines.
- `-s, --squeeze-blank`: squeeze repeated blank lines.

`rolio grep <pattern> [path]`

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

`rolio find [path]`

- `--name`: filename glob.
- `--iname`: case-insensitive filename glob.
- `-t, --type`: filter by type, `f` or `file` for files, `d` or `dir` for dirs.
- `--maxdepth`: maximum descent depth. `0` means unlimited.
- `--mindepth`: minimum descent depth.
- `-a, --all`: include hidden files.

`rolio stat <path>`

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

`rolio write <path> [content]`

Creates or overwrites a file. Parent directories are created as needed. If
`content` is omitted, content is read from stdin.

`rolio edit <path> --old <text> --new <text> [--all]`

Replaces text in a file. By default only the first occurrence is replaced.
Use `--all` to replace every occurrence.

`rolio rm <path>`

Deletes a file or directory. Directory deletes are recursive.

`rolio search <query>`

Runs ranked full-text search in the current repository and prints matching
snippets. Unlike mounted browsing commands, search is repository discovery.

- `--path`: limit results to a path prefix.
- `--limit`, `--offset`: paginate results.
- `--json`: emit structured output.

`rolio locate <query>`

Performs ranked lexical lookup and emits `repo://` references suitable for
`rolio cat` or mounting.

- `--all-repos`: query every repository registered on the server.
- `--limit`: cap returned results.
- `--json`: emit structured output.

`rolio glob <pattern>`

Discovers document paths by glob, including `**` recursive matching.

- `--all-repos`: query every repository registered on the server.
- `--limit`, `--offset`: paginate results.
- `--long`: include size and modification time.

`rolio repo ls`

Lists repositories available from the configured server.

`rolio mount add <remote-ref> <local-path>`

Adds a mapping in `.rolio/mounts.toml` from a local path to
`repo://self/<path>` or `repo://<repo>/<path>` and refreshes the local
manifest unless disabled.

- `--mode readonly|writable`: controls whether local writes may flow through
  the mount. Defaults to `readonly`.
- `--force`: replace a mount at the same local path.
- `--no-refresh`: skip the post-add manifest refresh.

Use `rolio mount ls` to inspect mappings and `rolio mount rm <local-path>`
to remove a mapping after any materialized files beneath it have been
dematerialized.

`rolio mount attach <keyword-or-repo> --into <local-path>`

Finds a uniquely matching repository name and adds a read-only root mount.

- `--dry-run`: preview the resolved mount.
- `--force`: replace a mount already using the target local path.

`rolio init [path]`

Creates `.rolio/settings.toml` and `.rolio/mounts.toml` and, unless disabled,
injects a minimal ROLIO entry into an agent instruction file, installs the ROLIO
skill to user config dirs, and sets up hooks.

- default: write `AGENTS.md`, install skill to `~/.claude/skills/rolio-skill/SKILL.md`
  and `~/.codex/skills/rolio-skill/SKILL.md`, install hooks to `~/.claude/` and `~/.codex/`
- `--repo <name>`: set the logical repository name
- `--server <url>`: set the `rolio-server` base URL
- `--mode md|skill|md,skill`: choose Markdown instructions, skill output,
  or both; default is `md,skill`
- `--agent claude`: write `CLAUDE.md`
- `--register`: register the repo with `rolio-server`
- `--no-instructions`: write config only
- `--hook codex`: install user-level Codex hooks in `~/.codex/`
- `--hook claude`: install user-level Claude Code hooks in `~/.claude/`
- `--hook codex --scope project`: install project Codex hooks in `.codex/`
- `--hook claude --scope project`: install project Claude Code hooks in
  `.claude/`

The injected block is wrapped with:

```markdown
<!-- ROLIO_START -->
...
<!-- ROLIO_END -->
```

Running `rolio init` again replaces the existing block instead of appending a
duplicate.

`rolio sync push <local-path>`

Scans a local file or directory, uploads each file through the existing ROLIO
write API, and updates `.rolio/manifest.toml`.

- `--manifest`: custom manifest path. Defaults to `.rolio/manifest.toml`.

Phase 2A/2B stores client-computed `sha256:<hex>` hashes, file size, and mtime
in the manifest. It does not change the server API or database schema.

`rolio sync pull <local-path>`

Reads remote ROLIO docs under the given path and updates `.rolio/manifest.toml`.
By default it only refreshes the manifest; it does not write local files.

- `--manifest`: custom manifest path. Defaults to `.rolio/manifest.toml`.
- `--materialize`: write pulled docs to local files.
- `--force-local`: resolve conflicts by pushing local content back to ROLIO.
- `--force-remote`: resolve conflicts by accepting remote content locally.

Conflict detection compares the manifest's last synced hash with current local
and remote hashes. If both changed, pull fails unless one force flag is used.

`rolio sync refresh <path>`

Refreshes `.rolio/manifest.toml` for remote docs under a path without writing
local files.

- `--manifest`: custom manifest path. Defaults to `.rolio/manifest.toml`.

`rolio sync materialize <path>`

Refreshes the manifest and writes remote docs under the path to local markdown
files.

- `--manifest`: custom manifest path. Defaults to `.rolio/manifest.toml`.

`rolio sync dematerialize <path>`

Marks manifest entries under the path as remote-only and removes local
materialized files.

- `--manifest`: custom manifest path. Defaults to `.rolio/manifest.toml`.
- `--keep-files`: update the manifest but leave local files in place.

`rolio docset <subcommand>`

Manages optional curated cross-repository docsets when enabled by the
configured server. Prefer `docs://` namespaces plus `rolio mount add` for
reusable documentation trees. Docset names accept lowercase letters,
digits, `-`, and `_`.

- `create <name> [--description <text>]`: create a docset.
- `list [--json]`: list docsets.
- `show <name> [--json]`: show members and their `docset://` references.
- `add <name> <docset-path> --source repo://<repo>/<path>`: add a source
  document at a stable docset path.
- `rm <name> <docset-path>`: remove a member.

Use `rolio cat docset://<name>/<path>` to read a docset member. Use
`rolio mount add docset://<name> <local-path>` to mount a read-only view of the
member tree. Change membership with `rolio docset add` and `rolio docset rm`;
use `docs://...` namespaces for writable reusable documentation trees.

`rolio config doctor`

Prints the configured repository selected for CLI requests.

`rolio hook session-start`

Refreshes manifest metadata and changed materialized files for agent session
startup. Installed Codex and Claude hooks call this operation with a timeout
so it does not block the session.

## Agent Usage

For agent workflows, treat ROLIO as the canonical way to browse shared project
docs. Prefer:

```bash
rolio tree /docs -L 3
rolio grep "topic" /docs
rolio cat /docs/file.md
```

over scanning local files when the information is meant to come from shared
internal documentation. The default docs root is `/docs`, configurable through
`[docs].path` in `.rolio/settings.toml`.
