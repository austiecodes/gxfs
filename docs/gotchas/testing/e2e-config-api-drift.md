# E2E fixtures can drift from internal config APIs

## Problem

`go test -count=1 -tags=e2e ./test/e2e` failed because older e2e fixtures still
initialized `postgres.Config{RepoTable: ...}` after the field was renamed to
`RepoNodesTable`. After that was fixed, the same fixtures failed migrations with
`unsafe identifier ""` because they did not populate `ContentTable` and
`Files` column mappings required by `SchemaSQL`.

## Cause

The e2e suite is outside normal package unit tests and uses build tags, so
plain `go test ./...` does not compile these files. Internal API refactors can
therefore leave tagged e2e fixtures stale until the e2e command is run. Also,
direct `postgres.Config` literals bypass the server config defaults that
normally fill table and column names.

## Solution

Run the tagged e2e compile/test command after changing shared test fixture
types or CLI command names:

```bash
go test -count=1 -tags=e2e ./test/e2e
```

Keep e2e fixtures aligned with the current internal API names, prefer shared
test config helpers over partial literals, and update command invocations when
compatibility aliases are removed.
