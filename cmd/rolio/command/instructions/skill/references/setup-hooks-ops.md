# Setup, Hooks, and Operations

Use this for project setup, agent integration, and server-side operations.

## Initialize a Project

```bash
rolio init --register --repo github.com/user/repo
rolio init --agent claude --register --repo github.com/user/repo
rolio init --mode md
rolio init --mode skill
rolio init --mode md,skill
rolio init --no-instructions
```

Default initialization writes `.rolio/settings.toml`, `.rolio/mounts.toml`, a minimal agent instruction block, and the local ROLIO skill.

## Config Files

- `.rolio/settings.toml`: repo name, server address, auth mode, and default docs path.
- `.rolio/mounts.toml`: local path to `repo://` or `docs://` source mappings.
- `.rolio/manifest.toml`: generated sync/materialization state.

The CLI reads project config and talks to `rolio-server`; it must not connect to PostgreSQL directly.

## Agent Hooks

```bash
rolio init --hook codex
rolio init --hook claude
rolio init --hook codex --scope project
rolio init --hook claude --scope project
```

Hooks refresh docs at session start and attach usage correlation to agent-driven ROLIO commands. Codex requires reviewing newly installed hooks before they run.

## Server Operations

Server config owns backend credentials. Start the server with:

```bash
ROLIO_SERVER_CONFIG=/etc/rolio/server.toml rolio-server
```

The server reads `conf/server.toml` when `ROLIO_SERVER_CONFIG` is unset.

## Orphan Content GC

GC is an out-of-band maintenance command on `rolio-server`, not a request sent by `rolio` to the HTTP server:

```bash
ROLIO_SERVER_CONFIG=/etc/rolio/server.toml rolio-server gc --dry-run
ROLIO_SERVER_CONFIG=/etc/rolio/server.toml rolio-server gc --force --grace-hours 24
```
