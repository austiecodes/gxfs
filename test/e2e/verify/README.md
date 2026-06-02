# e2e/verify — Manual E2E Verification Scripts

Shell-based end-to-end verification scripts for gxfs-server. These scripts
test real server behavior against a real PostgreSQL database (no mocks).

## Prerequisites

- PostgreSQL running on `localhost:5432`
- Your OS user can `createdb` (for test DB setup/teardown)
- Go toolchain (scripts auto-build `gxfs-server` if not found)
- `curl`, `python3` (for JSON parsing)

## Directory Structure

```
e2e/verify/
  lib.sh                    # Shared helpers (assertions, server lifecycle, DB ops)
  verify-writable-mount.sh  # Phase #14 - Cross-repo writable mount
  verify-locate.sh          # Phase #15A - Lexical locate
  verify-docset.sh          # Phase #16 - Docset API
  verify-gc.sh              # Phase #17 - Orphan doc GC
```

## Running

Run a single phase:

```bash
cd e2e/verify
chmod +x *.sh
./verify-docset.sh
```

Run all phases:

```bash
cd e2e/verify
for f in verify-*.sh; do
    echo "====== $f ======"
    ./"$f" || echo "FAILED: $f"
    echo ""
done
```

Pass a custom binary path:

```bash
./verify-docset.sh /path/to/gxfs-server
```

## Configuration

Environment variables (all optional, with defaults):

| Variable      | Default          | Description                    |
|---------------|------------------|--------------------------------|
| VERIFY_PORT   | 17635            | Server listen port             |
| VERIFY_DB     | gxfs_verify      | Test database name             |
| VERIFY_USER   | $(whoami)        | PostgreSQL user                |
| VERIFY_DIR    | /tmp/gxfs-verify | Working directory for configs  |

## What These Scripts Do

Each script:

1. **Resets** the test database (DROP + CREATE)
2. **Starts** a fresh `gxfs-server` instance with a generated config
3. **Seeds** test data via the server API
4. **Executes** each scenario (curl requests + assertions)
5. **Prints** PASS/FAIL per assertion with a final summary
6. **Stops** the server and exits non-zero on any failure

## Design Decisions

- **Real infrastructure only.** No mocks, no test doubles. These scripts exist
  to catch issues that unit tests miss (routing, encoding, middleware, DB state).
- **Self-contained.** Each script manages its own server lifecycle and DB state.
  No shared state between scripts.
- **Idempotent.** Can be re-run any number of times. DB is reset at the start.
- **Exit code.** 0 = all pass, 1 = at least one failure.

## Adding New Phases

1. Copy an existing `verify-*.sh` as a template
2. Source `lib.sh` at the top
3. Use `assert_status`, `assert_contains`, `assert_json_field` for checks
4. Use `curl_get`, `curl_put`, `curl_post`, `curl_delete` for HTTP calls
5. Use `db_exec` for direct DB queries when needed
6. Call `print_summary` at the end

## Relationship to e2e/ Go Tests

The Go tests in `e2e/*.go` (run via `go test -tags=e2e ./e2e/...`) are
Cindy's automated integration tests that run in Docker. These shell scripts
are complementary — they're designed for:

- Smoke testing on a real local environment (no Docker required)
- Exploratory verification of edge cases
- Regression testing after deploys
- Auditable verification records (script output is the proof)
