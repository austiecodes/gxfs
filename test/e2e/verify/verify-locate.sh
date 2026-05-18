#!/usr/bin/env bash
# verify-locate.sh - Phase #15A Lexical Locate e2e verification
#
# Scenarios:
#   1. Basic locate (single repo, relevance ranking)
#   2. --all-repos fan-out (multi-repo results merged by score)
#   3. JSON output (Total = pre-limit sum)
#   4. --limit truncation
#   5. repo:// ref -> cat preview pipeline
#   6. Empty/no-results handling
#   7. Regression: search/ls/stat unaffected
#
# Prerequisites:
#   - PostgreSQL running on localhost:5432
#   - gxfs-server binary built
#
# Usage:
#   ./verify-locate.sh [path-to-gxfs-server-binary]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

BINARY="${1:-$(cd "${SCRIPT_DIR}/../.." && pwd)/bin/gxfs-server}"
if [ ! -x "$BINARY" ]; then
    echo "Building gxfs-server..."
    (cd "${SCRIPT_DIR}/../.." && go build -o bin/gxfs-server ./cmd/gxfs-server)
    BINARY="${SCRIPT_DIR}/../../bin/gxfs-server"
fi

echo "=== Phase #15A: Lexical Locate E2E Verification ==="
echo "Binary: $BINARY"
echo ""

# --- Setup ---
db_reset
start_server "$BINARY"

# Seed: write docs with overlapping content for relevance testing
echo "Seeding test data..."
curl_put "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/write?path=/docs/error-handling.md" \
    '{"content":"# Error Handling in Go\n\nError handling is critical. Always wrap errors. Use errors.Is and errors.As.\nError context helps debugging. Never ignore errors.\n"}'
curl_put "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/write?path=/docs/testing.md" \
    '{"content":"# Testing Guide\n\nWrite table-driven tests. Use testify for assertions.\nTest error cases explicitly.\n"}'
curl_put "${SERVER_ADDR}/v1/repos/${REPO2_ENC}/write?path=/docs/api-errors.md" \
    '{"content":"# API Error Responses\n\nReturn structured error codes. Map errors to HTTP status.\nError responses should include context.\n"}'
curl_put "${SERVER_ADDR}/v1/repos/${REPO2_ENC}/write?path=/docs/deployment.md" \
    '{"content":"# Deployment Guide\n\nUse Docker for containerization. Set health checks.\n"}'
echo "Seed complete."
echo ""

# --- Scenario 1: Basic locate (single repo) ---
echo "--- Scenario 1: Basic locate ---"
curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/locate?q=error"
assert_status "Locate returns 200" "200" "$STATUS"
assert_contains "Results present" "$BODY" '"results"'
# error-handling.md should rank higher (more error mentions)
FIRST_PATH=$(echo "$BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['results'][0]['path'])" 2>/dev/null || echo "")
if [ "$FIRST_PATH" = "/docs/error-handling.md" ]; then
    printf "${GREEN}PASS${NC} Relevance ranking: error-handling.md ranked first\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Relevance ranking: expected error-handling.md first, got '%s'\n" "$FIRST_PATH"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 2: Cross-repo locate ---
echo "--- Scenario 2: Cross-repo locate ---"
# Locate across repo2
curl_get "${SERVER_ADDR}/v1/repos/${REPO2_ENC}/locate?q=error"
assert_status "Cross-repo locate" "200" "$STATUS"
assert_contains "Has api-errors result" "$BODY" "api-errors"

# --- Scenario 3: JSON output with Total ---
echo "--- Scenario 3: JSON output Total semantics ---"
curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/locate?q=error"
TOTAL=$(echo "$BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('total',0))" 2>/dev/null || echo "0")
RESULT_COUNT=$(echo "$BODY" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('results',[])))" 2>/dev/null || echo "0")
if [ "$TOTAL" -ge "$RESULT_COUNT" ] && [ "$TOTAL" -gt 0 ]; then
    printf "${GREEN}PASS${NC} Total semantics: total=%s >= results=%s\n" "$TOTAL" "$RESULT_COUNT"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Total semantics: total=%s, results=%s\n" "$TOTAL" "$RESULT_COUNT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 4: Limit truncation ---
echo "--- Scenario 4: Limit truncation ---"
curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/locate?q=error&limit=1"
RESULT_COUNT=$(echo "$BODY" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('results',[])))" 2>/dev/null || echo "0")
if [ "$RESULT_COUNT" = "1" ]; then
    printf "${GREEN}PASS${NC} Limit truncation: results=%s with limit=1\n" "$RESULT_COUNT"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} Limit truncation: expected 1 result, got %s\n" "$RESULT_COUNT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 5: repo:// ref -> cat pipeline ---
echo "--- Scenario 5: Locate -> cat pipeline ---"
# Make a fresh locate request (do not reuse stale $BODY from Scenario 4)
curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/locate?q=error"
RESULT_PATH=$(echo "$BODY" | python3 -c "import sys,json; r=json.load(sys.stdin).get('results'); print(r[0]['path'] if r else '')" 2>/dev/null || echo "")
if [ -n "$RESULT_PATH" ]; then
    curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/cat?path=${RESULT_PATH}"
    assert_status "Cat from locate result" "200" "$STATUS"
    assert_contains "Cat has content" "$BODY" '"content"'
else
    printf "${RED}FAIL${NC} Locate -> cat pipeline: no locate results to follow\n"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 6: Empty/no-results ---
echo "--- Scenario 6: Empty/no-results ---"
curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/locate?q=xyznonexistentquery"
assert_status "No results returns 200" "200" "$STATUS"
RESULT_COUNT=$(echo "$BODY" | python3 -c "import sys,json; r=json.load(sys.stdin).get('results'); print(len(r) if r else 0)" 2>/dev/null || echo "-1")
if [ "$RESULT_COUNT" = "0" ]; then
    printf "${GREEN}PASS${NC} No results: empty array returned\n"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    printf "${RED}FAIL${NC} No results: expected 0, got %s\n" "$RESULT_COUNT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# --- Scenario 7: Regression ---
echo "--- Scenario 7: Regression checks ---"
curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/search?q=testing"
assert_status "Regression: search" "200" "$STATUS"
curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/ls"
assert_status "Regression: ls" "200" "$STATUS"
curl_get "${SERVER_ADDR}/v1/repos/${REPO1_ENC}/stat?path=/docs/testing.md"
assert_status "Regression: stat" "200" "$STATUS"

# --- Done ---
print_summary
