#!/usr/bin/env bash
# verify-gc.sh - Phase #17 Orphan Doc GC e2e verification
#
# Scenarios:
#   1. Dry-run preview (identifies orphans, does not delete)
#   2. Force delete (removes orphans, referenced docs safe)
#   3. Grace period protection (fresh orphan excluded)
#   4. DSN redaction (password not leaked in output)
#   5. Infra backend target count
#   6. Zero-target error handling
#
# Prerequisites:
#   - PostgreSQL running on localhost:5432
#   - gxfs-server binary built
#
# Usage:
#   ./verify-gc.sh [path-to-gxfs-server-binary]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

BINARY="${1:-$(cd "${SCRIPT_DIR}/../.." && pwd)/bin/gxfs-server}"
if [ ! -x "$BINARY" ]; then
    echo "Building gxfs-server..."
    (cd "${SCRIPT_DIR}/../.." && go build -o bin/gxfs-server ./cmd/gxfs-server)
    BINARY="${SCRIPT_DIR}/../../bin/gxfs-server"
fi

echo "=== Phase #17: Orphan Doc GC E2E Verification ==="
echo "Binary: $BINARY"
echo ""

# --- Setup ---
db_reset
start_server "$BINARY"

# Seed: write some docs
echo "Seeding test data..."
curl_put "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/write?path=/docs/keep-me.md" \
    '{"content":"# Keep This\n\nThis doc has a repo_path reference.\n"}'
curl_put "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/write?path=/docs/orphan1.md" \
    '{"content":"# Orphan 1\n\nWill become orphan.\n"}'
curl_put "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/write?path=/docs/orphan2.md" \
    '{"content":"# Orphan 2\n\nWill become orphan.\n"}'
echo "Seed complete."

# Get doc IDs
ORPHAN1_ID=$(db_exec "SELECT doc_id FROM gxfs_repo_paths WHERE path = '/docs/orphan1.md' AND repo = '${REPO1}';")
ORPHAN2_ID=$(db_exec "SELECT doc_id FROM gxfs_repo_paths WHERE path = '/docs/orphan2.md' AND repo = '${REPO1}';")
KEEP_ID=$(db_exec "SELECT doc_id FROM gxfs_repo_paths WHERE path = '/docs/keep-me.md' AND repo = '${REPO1}';")

# Validate IDs extracted
for var_name in ORPHAN1_ID ORPHAN2_ID KEEP_ID; do
    eval "val=\$$var_name"
    if [ -z "$val" ]; then
        echo "FATAL: could not extract $var_name from DB. Seed failed?"
        exit 1
    fi
done
# Remove repo_path references to create orphans
db_exec "DELETE FROM gxfs_repo_paths WHERE doc_id IN ('${ORPHAN1_ID}', '${ORPHAN2_ID}');"
# Age orphans past grace period
db_exec "UPDATE gxfs_docs SET updated_at = NOW() - INTERVAL '2 hours' WHERE id IN ('${ORPHAN1_ID}', '${ORPHAN2_ID}');"

echo ""

# --- Scenario 1: Dry-run preview ---
echo "--- Scenario 1: Dry-run preview ---"
GC_OUT=$(GXFS_SERVER_CONFIG="$SERVER_CONFIG" "$BINARY" gc --dry-run --limit 10 2>&1 || true)
if echo "$GC_OUT" | grep -q "2 orphan"; then
    printf "${GREEN}PASS${NC} Dry-run identifies 2 orphans\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Dry-run did not find 2 orphans\n"
    printf "  Output: %s\n" "$GC_OUT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi
# Verify docs still exist (dry-run should not delete)
DOC_COUNT=$(db_exec "SELECT COUNT(*) FROM gxfs_docs WHERE id IN ('${ORPHAN1_ID}', '${ORPHAN2_ID}');")
if [ "$DOC_COUNT" = "2" ]; then
    printf "${GREEN}PASS${NC} Dry-run does not delete\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Dry-run deleted docs (count=%s, expected 2)\n" "$DOC_COUNT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 2: Force delete ---
echo "--- Scenario 2: Force delete ---"
GC_OUT=$(GXFS_SERVER_CONFIG="$SERVER_CONFIG" "$BINARY" gc --force 2>&1 || true)
if echo "$GC_OUT" | grep -q "Deleted 2"; then
    printf "${GREEN}PASS${NC} Force deleted 2 orphans\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Force did not delete 2 orphans\n"
    printf "  Output: %s\n" "$GC_OUT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi
# Referenced doc still exists
KEEP_EXISTS=$(db_exec "SELECT COUNT(*) FROM gxfs_docs WHERE id = '${KEEP_ID}';")
if [ "$KEEP_EXISTS" = "1" ]; then
    printf "${GREEN}PASS${NC} Referenced doc preserved\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Referenced doc was deleted!\n"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 3: Grace period protection ---
echo "--- Scenario 3: Grace period protection ---"
# Create a fresh orphan (updated_at = now)
curl_put "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/write?path=/docs/fresh-orphan.md" \
    '{"content":"# Fresh\n\nJust created.\n"}'
FRESH_ID=$(db_exec "SELECT doc_id FROM gxfs_repo_paths WHERE path = '/docs/fresh-orphan.md' AND repo = '${REPO1}';")
db_exec "DELETE FROM gxfs_repo_paths WHERE doc_id = '${FRESH_ID}';"
# Do NOT age it - it should be protected by grace period
GC_OUT=$(GXFS_SERVER_CONFIG="$SERVER_CONFIG" "$BINARY" gc --force 2>&1 || true)
FRESH_EXISTS=$(db_exec "SELECT COUNT(*) FROM gxfs_docs WHERE id = '${FRESH_ID}';")
if [ "$FRESH_EXISTS" = "1" ]; then
    printf "${GREEN}PASS${NC} Fresh orphan protected by grace period\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Fresh orphan was incorrectly deleted!\n"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 4: DSN redaction ---
echo "--- Scenario 4: DSN redaction ---"
# Write a config with password in DSN
DSN_CONFIG="${VERIFY_DIR}/dsn-test.toml"
cat > "$DSN_CONFIG" <<TOML
addr = ":19999"

[backend]
type = "doc_postgres"

[backend.postgres]
dsn = "postgresql://myuser:supersecret@localhost:5432/${VERIFY_DB}"
schema = "public"
TOML
GC_OUT=$(GXFS_SERVER_CONFIG="$DSN_CONFIG" "$BINARY" gc --dry-run 2>&1 || true)
if echo "$GC_OUT" | grep -q "supersecret"; then
    printf "${RED}FAIL${NC} DSN redaction: password leaked in output!\n"
    FAIL_COUNT=$((FAIL_COUNT + 1))
else
    printf "${GREEN}PASS${NC} DSN redaction: password not in output\n"
    PASS_COUNT=$((PASS_COUNT + 1))
fi

# --- Scenario 5: Infra backend target count ---
echo "--- Scenario 5: Infra backend target count ---"
# Infra-only config points to one DSN+schema -> should produce 1 target.
DEDUP_CONFIG="${VERIFY_DIR}/dedup-test.toml"
cat > "$DEDUP_CONFIG" <<TOML
addr = ":19999"

[backend]
type = "doc_postgres"

[backend.postgres]
dsn = "postgresql://${VERIFY_USER}@localhost:5432/${VERIFY_DB}"
schema = "public"
TOML
GC_OUT=$(GXFS_SERVER_CONFIG="$DEDUP_CONFIG" "$BINARY" gc --dry-run 2>&1 || true)
TARGET_COUNT=$(echo "$GC_OUT" | grep -c "Target " || true)
if [ "$TARGET_COUNT" = "1" ]; then
    printf "${GREEN}PASS${NC} Infra backend target count: 1 backend -> 1 target\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Infra backend target count: expected 1 target, got %s\n" "$TARGET_COUNT"
    printf "  Output: %s\n" "$GC_OUT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 6: Zero-target error ---
echo "--- Scenario 6: Zero-target error ---"
NOTARGET_CONFIG="${VERIFY_DIR}/notarget-test.toml"
cat > "$NOTARGET_CONFIG" <<TOML
addr = ":19999"

[backend]
type = "postgres"

[backend.postgres]
dsn = "postgresql://${VERIFY_USER}@localhost:5432/${VERIFY_DB}"
schema = "public"
nodes_table = "vfs_nodes"
content_table = "vfs_content"
TOML
GC_OUT=$(GXFS_SERVER_CONFIG="$NOTARGET_CONFIG" "$BINARY" gc --dry-run 2>&1 || true)
if [[ "$GC_OUT" == *"no doc_postgres storage targets configured"* ]]; then
    printf "${GREEN}PASS${NC} Zero-target: error message correct\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Zero-target: unexpected output\n"
    printf "  Output: %s\n" "$GC_OUT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Done ---
print_summary
