# GXFS Phase 1 Foundation Plan

> This is a review plan, not an execution playbook. It intentionally avoids implementation code so we can review architecture, sequencing, and scope before building.

**Goal:** Land the first foundation slice from the roadmap: config-free init, split settings/mounts config, current-repo mount resolution, corrected cache TTL semantics, and race-oriented verification.

**Architecture:** Keep `.gxfs/settings.toml` as stable base config, `.gxfs/mounts.toml` as desired mount state, and reserve `.gxfs/manifest.toml` for later generated resolved state. Phase 1 supports only current-repo mounts with `repo://self/<path>`; reusable cross-repo knowledge remains a later namespace/search/collection design.

**Tech Stack:** Go, Cobra, go-toml/v2, existing `store.Adapter` interfaces, existing HTTP client/server flow, Postgres adapter, standard Go tests.

---

## Scope

### In Scope

- `gxfs init` must run without an existing `.gxfs/settings.toml`.
- `gxfs init` should generate both `.gxfs/settings.toml` and `.gxfs/mounts.toml`.
- `.gxfs/settings.toml` should hold stable connection/base policy.
- `.gxfs/mounts.toml` should hold desired local-to-remote mounts.
- The CLI should resolve local command paths through mounts before calling the backend.
- Phase 1 mount refs support `repo://self/<path>` only.
- `collection://...` and true cross-repo repo refs should be detected and rejected with clear errors.
- Empty Postgres `cache_ttl` should mean "cache until invalidated".
- Verification should include normal Go tests and race tests.

### Out of Scope

- `gxfs search`
- `gxfs sync`
- `.gxfs/manifest.toml` generation
- Materialized local markdown files
- DB redesign
- Multi-repo server registry
- `collection://...` implementation
- Auth enforcement and audit logging

---

## Proposed File Responsibilities

### CLI

- `cmd/gxfs/main.go`
  - Split config-free command handling from normal configured command handling.
  - Add init flags for repo, server, docs root, auth mode, and agent target.
  - Generate both config files during init.
  - Load settings and mounts during normal command execution.
  - Wrap the existing HTTP client with mount resolution.

- `cmd/gxfs/main_test.go`
  - Cover `init` without config.
  - Cover generated settings/mounts templates.
  - Cover that configured commands still fail clearly when config is missing.
  - Cover basic path translation behavior through command execution.

- `cmd/gxfs/instructions/agents.md`
  - Mention `.gxfs/mounts.toml` as the place mounted docs are declared.

### Config

- `internal/config/config.go`
  - Extend CLI settings with `version`, `auth`, and `cache`.
  - Add `MountsConfig` and `MountConfig`.
  - Add `LoadMounts`.
  - Keep legacy `[mount] include/exclude` parsing temporarily so old configs do not break immediately.
  - Add default mounts derived from `docs.path` when `.gxfs/mounts.toml` is absent.

- `internal/config/config_test.go`
  - Cover new settings format.
  - Cover mount parsing and validation.
  - Cover default mount generation.
  - Cover legacy config compatibility.

### Mount Resolution

- `internal/mount/resolver.go`
  - Resolve local paths to remote repo/path pairs.
  - Choose longest matching local mount prefix.
  - Enforce read-only vs writable mount mode.
  - Translate remote response paths back to local paths.
  - Reject unsupported remote refs in Phase 1.

- `internal/mount/adapter.go`
  - Implement a `store.Adapter` wrapper around the existing client.
  - Translate request paths before forwarding.
  - Translate response paths before printing.
  - Return a permission error for writes to read-only mounts.

- `internal/mount/*_test.go`
  - Cover longest-prefix resolution.
  - Cover root/current-repo mount behavior.
  - Cover read-only write rejection.
  - Cover unsupported `collection://...` rejection.
  - Cover response path localization.

### Store and Server

- `internal/store/store.go`
  - Add a sentinel error for read-only mount writes.

- `internal/server/server.go`
  - Map read-only mount errors to HTTP 403.

### Postgres Cache

- `internal/store/postgres/adapter.go`
  - Fix zero TTL semantics.
  - Reduce mutable cached tree race risk around lazy content loading and writes.

- `internal/store/postgres/adapter_test.go`
  - Cover zero TTL and positive TTL behavior.

---

## Task Breakdown

### Task 1: Config-Free Init

**Intent:** `gxfs init` should be usable as the first command in a repo.

**Changes:**

- Treat `init`, `help`, `--help`, and `-h` as config-free command paths.
- Keep normal commands config-dependent.
- Preserve current help behavior.

**Tests:**

- `gxfs init <tmpdir> --no-instructions` succeeds when `GXFS_CONFIG` points to a missing file.
- `gxfs --help` succeeds when config is missing.
- `gxfs config doctor` still fails clearly when config is missing.

**Acceptance Criteria:**

- Fresh repo bootstrap works.
- Config-dependent commands do not silently run with fake defaults.

---

### Task 2: Init Templates for Settings and Mounts

**Intent:** `gxfs init` should produce a self-hosting-friendly starting point.

**Changes:**

- Generate `.gxfs/settings.toml`.
- Generate `.gxfs/mounts.toml`.
- Add init flags:
  - `--repo`
  - `--server`
  - `--docs`
  - `--auth`
  - existing agent flags remain
- Default docs root should be `docs`.
- Default mount should map local `docs` to `repo://self/docs`.

**Tests:**

- Generated settings include repo, server, auth, docs, and cache fields.
- Generated mounts include one writable default mount.
- Existing files are not overwritten.
- Invalid auth mode fails with a clear error.

**Acceptance Criteria:**

- A human can run `gxfs init`, edit the server address/token env if needed, and understand where mounts live.

---

### Task 3: Settings and Mounts Parsing

**Intent:** Separate base config from mount state in code.

**Changes:**

- Parse new settings fields:
  - `version`
  - `auth.mode`
  - `auth.token_env`
  - `cache.metadata_ttl`
  - `cache.content_ttl`
  - `cache.materialize`
- Parse `.gxfs/mounts.toml`.
- Validate mount `local`, `remote`, and `mode`.
- Default mode for explicit mounts should be `readonly` unless set.
- Generate a default writable docs mount if `mounts.toml` is missing.
- Keep old `[mount] include/exclude` parsing for now as legacy compatibility.

**Tests:**

- New settings format loads.
- Legacy settings format still loads.
- Valid mounts load.
- Invalid mount mode fails.
- Missing mounts file produces default docs mount.

**Acceptance Criteria:**

- Settings and mounts have distinct structs and loaders.
- Existing users are not broken just by updating the CLI.

---

### Task 4: Mount Resolver

**Intent:** Local CLI paths should map to existing backend paths without changing the server API yet.

**Changes:**

- Add `internal/mount` resolver.
- Resolve paths by longest local prefix.
- Support only `repo://self/<path>` in Phase 1.
- Reject `collection://...` and non-self repo refs with explicit errors.
- Track mount mode for write checks.
- Translate remote response paths back to local paths.

**Tests:**

- Longest-prefix mount wins.
- `docs/foo.md` maps through the default docs mount.
- Read-only mounts reject write operations.
- Unsupported remote refs fail clearly.
- Remote response paths map back to local paths.

**Acceptance Criteria:**

- The CLI can present a mounted local tree while still using the current backend path model.

---

### Task 5: Mount Adapter Wrapper

**Intent:** Apply mount resolution transparently to existing CLI commands.

**Changes:**

- Add an adapter wrapper implementing `store.Adapter`.
- Wrap the existing HTTP client in normal CLI runtime.
- Translate request paths for:
  - `ls`
  - `tree`
  - `cat`
  - `grep`
  - `find`
  - `stat`
  - `write`
  - `edit`
  - `delete`
- Translate response paths for list/search/read/stat outputs.
- Reject writes to read-only mounts before sending the backend request.

**Tests:**

- Read operations call the backend with remote paths.
- Output paths are local paths.
- Write operations on writable mounts pass through.
- Write operations on read-only mounts fail locally.

**Acceptance Criteria:**

- Existing command UX stays Unix-like.
- Mount behavior is centralized rather than scattered across commands.

---

### Task 6: Read-Only Error Mapping

**Intent:** Permission failures should have stable semantics.

**Changes:**

- Add a `store.ErrReadOnlyMount` sentinel.
- Map it to HTTP 403 in the server error mapper.

**Tests:**

- Local adapter returns the sentinel error.
- Server maps the sentinel error to 403.

**Acceptance Criteria:**

- Read-only mount rejection is distinguishable from not found or bad request.

---

### Task 7: Postgres Cache TTL Semantics

**Intent:** Match documented cache behavior.

**Changes:**

- `cache_ttl` unset or zero means cache until invalidated.
- Positive `cache_ttl` expires after the configured duration.
- Guard lazy content mutation and write mutation enough for race verification.

**Tests:**

- Zero TTL does not expire.
- Positive TTL expires.
- Existing Postgres adapter tests continue to pass.

**Acceptance Criteria:**

- README/server config behavior and code behavior match.

---

### Task 8: Verification

**Intent:** Confirm foundation changes do not destabilize existing behavior.

**Commands:**

- `go test ./...`
- `go test -race ./...`
- Optional e2e check if local Docker/Postgres is available:
  - `go test -tags=e2e ./e2e -run TestGXFSPostgresServerCLI -count=1`

**Acceptance Criteria:**

- Normal tests pass.
- Race tests pass or expose a documented race that is fixed in this phase.
- E2E is either passing or explicitly skipped with the reason.

---

## Review Decisions Needed

1. **Phase 1 remote syntax:** Use `repo://self/<path>` only for now.
2. **Unsupported refs:** Reject `collection://...` and non-self repo refs in Phase 1.
3. **Missing mounts file:** Fall back to a generated default docs mount instead of failing.
4. **Legacy config:** Keep `[mount] include/exclude` parsing for now, but do not build new behavior on it.
5. **Read-only enforcement:** Enforce locally in Phase 1; server-side auth remains later work.

---

## Follow-Up Roadmap Items

- Define final cross-repo repo reference syntax.
- Implement `collection://...` refs.
- Add search and mount commands.
- Generate `.gxfs/manifest.toml`.
- Redesign DB around docs, collections, mounts, and revisions.
- Add real auth, permission checks, and audit logs.

