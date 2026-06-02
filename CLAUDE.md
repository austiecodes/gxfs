# AGENTS

## Document Index

- `docs/dev/README.md` - developer and operator guide: server configuration,
  maintenance commands, build/test workflow, and package layout
- `docs/dev/cli-command-refactor.md` - command shape and agent guidance design
- `docs/gotchas/` - pitfall notes. Create subdirectories by topic, one
  Markdown file per pitfall, using the format: problem -> cause -> solution
- `cmd/gxfs/command/instructions/skill.md` - generated GXFS skill index
- `cmd/gxfs/command/instructions/skill/references/` - generated GXFS skill
  scenario references

## Documentation Update Rules

- Add or update a file under `docs/gotchas/` whenever you hit a non-obvious
  bug, tooling issue, integration trap, flaky behavior, or debugging lesson
  that is likely to waste time again if left undocumented.
- Treat `docs/gotchas/` as a required follow-up for real pitfalls encountered
  during implementation or testing, not as optional extra documentation.

## GXFS Quick Use

Use `gxfs` like a Unix-style virtual filesystem for shared docs:

```bash
gxfs ls docs
gxfs tree docs -L 3
gxfs cat docs/foo.md
gxfs grep "pattern" docs
gxfs find docs --name "*.md"
```

For discovery, mounts, sync, writes, hooks, or operations, use the GXFS skill
instead of expanding AGENTS.md. In this repo, read
`cmd/gxfs/command/instructions/skill.md` and then only the relevant scenario
file under `cmd/gxfs/command/instructions/skill/references/`. Generated repos
should load `.gxfs/skills/gxfs/SKILL.md` when present.

## Build & Test

```bash
go test ./...
go test ./internal/store
go test ./internal/vfs -run TestGrep
go build ./cmd/gxfs
go build ./cmd/gxfs-server
```

## Project Boundaries

- `cmd/gxfs` is the thin Cobra CLI. It reads `.gxfs/settings.toml` and talks to
  the server through HTTP; it must not connect to the database directly.
- `cmd/gxfs-server` owns backend configuration, store adapters, and HTTP APIs.
- `internal/store/store.go` defines the adapter capability boundary. Every
  adapter must include `var _ store.Adapter = (*Adapter)(nil)`.
- CLI config must not contain backend credentials. Server config owns storage
  connection details.
