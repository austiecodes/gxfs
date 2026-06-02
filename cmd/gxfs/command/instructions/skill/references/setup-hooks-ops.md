# Setup, Hooks, and Operations

Use this for project setup, agent integration, and server-side operations.

## Initialize a Project

```bash
gxfs init --register --repo github.com/user/repo
gxfs init --agent claude --register --repo github.com/user/repo
gxfs init --mode md
gxfs init --mode skill
gxfs init --mode md,skill
gxfs init --no-instructions
```

Default initialization writes `.gxfs/settings.toml`, `.gxfs/mounts.toml`, a minimal agent instruction block, and the local GXFS skill.

## Config Files

- `.gxfs/settings.toml`: repo name, server address, auth mode, and default docs path.
- `.gxfs/mounts.toml`: local path to `repo://` or `docs://` source mappings.
- `.gxfs/manifest.toml`: generated sync/materialization state.

The CLI reads project config and talks to `gxfs-server`; it must not connect to PostgreSQL directly.

## Agent Hooks

```bash
gxfs init --hook codex
gxfs init --hook claude
gxfs init --hook codex --scope project
gxfs init --hook claude --scope project
```

Hooks refresh docs at session start and attach usage correlation to agent-driven GXFS commands. Codex requires reviewing newly installed hooks before they run.

## Server Operations

Server config owns backend credentials. Start the server with:

```bash
GXFS_SERVER_CONFIG=/etc/gxfs/server.toml gxfs-server
```

The server reads `conf/server.toml` when `GXFS_SERVER_CONFIG` is unset.

## Orphan Content GC

GC is an out-of-band maintenance command on `gxfs-server`, not a request sent by `gxfs` to the HTTP server:

```bash
GXFS_SERVER_CONFIG=/etc/gxfs/server.toml gxfs-server gc --dry-run
GXFS_SERVER_CONFIG=/etc/gxfs/server.toml gxfs-server gc --force --grace-hours 24
```
