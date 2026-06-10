#!/usr/bin/env bash
# lib.sh - shared helpers for e2e verification scripts
# Source this file from each verify-*.sh script.

set -euo pipefail

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# --- Counters ---
PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0

# --- Config ---
VERIFY_PORT="${VERIFY_PORT:-17635}"
VERIFY_DB="${VERIFY_DB:-rolio_verify}"
VERIFY_USER="${VERIFY_USER:-$(whoami)}"
VERIFY_DIR="${VERIFY_DIR:-/tmp/rolio-verify}"
SERVER_ADDR="http://127.0.0.1:${VERIFY_PORT}"
REPO1="github.com/test/rolio-test-repo"
REPO2="github.com/test/other-repo"
SERVER_PID=""
SERVER_LOG="${VERIFY_DIR}/server.log"
SERVER_CONFIG="${VERIFY_DIR}/server.toml"

# --- Assertions ---

assert_status() {
    local desc="$1"
    local expected="$2"
    local actual="$3"
    if [ "$expected" = "$actual" ]; then
        printf "${GREEN}PASS${NC} %s (HTTP %s)\n" "$desc" "$actual"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        printf "${RED}FAIL${NC} %s (expected HTTP %s, got %s)\n" "$desc" "$expected" "$actual"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

assert_contains() {
    local desc="$1"
    local body="$2"
    local pattern="$3"
    if printf '%s' "$body" | grep -qF "$pattern"; then
        printf "${GREEN}PASS${NC} %s (body contains '%s')\n" "$desc" "$pattern"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        printf "${RED}FAIL${NC} %s (body missing '%s')\n" "$desc" "$pattern"
        printf "  Body: %s\n" "$body"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

assert_not_contains() {
    local desc="$1"
    local body="$2"
    local pattern="$3"
    if printf '%s' "$body" | grep -qF "$pattern"; then
        printf "${RED}FAIL${NC} %s (body unexpectedly contains '%s')\n" "$desc" "$pattern"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    else
        printf "${GREEN}PASS${NC} %s (body does not contain '%s')\n" "$desc" "$pattern"
        PASS_COUNT=$((PASS_COUNT + 1))
    fi
}

assert_json_field() {
    local desc="$1"
    local body="$2"
    local field="$3"
    local expected="$4"
    local actual
    actual=$(echo "$body" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d${field})" 2>/dev/null || echo "__PARSE_ERROR__")
    if [ "$actual" = "$expected" ]; then
        printf "${GREEN}PASS${NC} %s (%s = %s)\n" "$desc" "$field" "$actual"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        printf "${RED}FAIL${NC} %s (expected %s = '%s', got '%s')\n" "$desc" "$field" "$expected" "$actual"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

skip() {
    local desc="$1"
    local reason="$2"
    printf "${YELLOW}SKIP${NC} %s (%s)\n" "$desc" "$reason"
    SKIP_COUNT=$((SKIP_COUNT + 1))
}

# --- HTTP helpers ---

urlencode() {
    python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"
}

repo_url() {
    local repo="$1"
    local op="$2"
    local query="${3:-}"
    local encoded_repo
    encoded_repo="$(urlencode "$repo")"
    local url="${SERVER_ADDR}/v1/repos/${op}?repo=${encoded_repo}"
    if [ -n "$query" ]; then
        url="${url}&${query}"
    fi
    printf '%s' "$url"
}

# curl_get URL -> sets BODY and STATUS
curl_get() {
    local url="$1"
    local tmpfile="${VERIFY_DIR}/.resp_body.$$"
    STATUS=$(curl -s -o "$tmpfile" -w "%{http_code}" "$url") || STATUS="000"
    BODY=$(cat "$tmpfile" 2>/dev/null || true)
    rm -f "$tmpfile"
}

# curl_post URL DATA -> sets BODY and STATUS
curl_post() {
    local url="$1"
    local data="$2"
    local tmpfile="${VERIFY_DIR}/.resp_body.$$"
    STATUS=$(curl -s -o "$tmpfile" -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "$data" "$url") || STATUS="000"
    BODY=$(cat "$tmpfile" 2>/dev/null || true)
    rm -f "$tmpfile"
}

# curl_put URL DATA -> sets BODY and STATUS
curl_put() {
    local url="$1"
    local data="$2"
    local tmpfile="${VERIFY_DIR}/.resp_body.$$"
    STATUS=$(curl -s -o "$tmpfile" -w "%{http_code}" -X PUT -H "Content-Type: application/json" -d "$data" "$url") || STATUS="000"
    BODY=$(cat "$tmpfile" 2>/dev/null || true)
    rm -f "$tmpfile"
}

# curl_delete URL -> sets BODY and STATUS
curl_delete() {
    local url="$1"
    local tmpfile="${VERIFY_DIR}/.resp_body.$$"
    STATUS=$(curl -s -o "$tmpfile" -w "%{http_code}" -X DELETE "$url") || STATUS="000"
    BODY=$(cat "$tmpfile" 2>/dev/null || true)
    rm -f "$tmpfile"
}

# --- Server lifecycle ---

write_server_config() {
    mkdir -p "$VERIFY_DIR"
    cat > "$SERVER_CONFIG" <<TOML
addr = ":${VERIFY_PORT}"

[backend]
type = "doc_postgres"

[backend.postgres]
dsn = "postgresql://${VERIFY_USER}@localhost:5432/${VERIFY_DB}"
schema = "public"

[registry]
refresh_interval = "1s"
TOML
}

register_repo() {
    local repo="$1"
    local tmpfile="${VERIFY_DIR}/.register_repo.$$"
    local status
    status=$(curl -s -o "$tmpfile" -w "%{http_code}" \
        -X POST -H "Content-Type: application/json" \
        -d "{\"name\":\"${repo}\",\"writable\":true}" \
        "${SERVER_ADDR}/v1/repos") || status="000"
    if [ "$status" != "201" ] && [ "$status" != "409" ]; then
        echo "ERROR: failed to register repo ${repo} (HTTP ${status})"
        cat "$tmpfile" 2>/dev/null || true
        rm -f "$tmpfile"
        exit 1
    fi
    rm -f "$tmpfile"
}

start_server() {
    local binary="$1"
    write_server_config
    ROLIO_SERVER_CONFIG="$SERVER_CONFIG" "$binary" > "$SERVER_LOG" 2>&1 &
    SERVER_PID=$!
    # Wait for server to be ready
    local retries=30
    while ! curl -s "${SERVER_ADDR}/healthz" > /dev/null 2>&1; do
        ((retries--))
        if [ $retries -le 0 ]; then
            echo "ERROR: server failed to start within 3s"
            cat "$SERVER_LOG"
            exit 1
        fi
        sleep 0.1
    done
    register_repo "$REPO1"
    register_repo "$REPO2"
    echo "Server started (PID $SERVER_PID) on port $VERIFY_PORT"
}

stop_server() {
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
        echo "Server stopped"
    fi
}

# --- DB helpers ---

db_exec() {
    psql "postgresql://${VERIFY_USER}@localhost:5432/${VERIFY_DB}" -qtAX -c "$1" 2>/dev/null
}

db_reset() {
    # Drop and recreate test database
    if ! psql "postgresql://${VERIFY_USER}@localhost:5432/postgres" -qtAX -c "DROP DATABASE IF EXISTS ${VERIFY_DB};" 2>&1; then
        echo "ERROR: failed to drop database ${VERIFY_DB}. Is PostgreSQL running? Does user have privileges?"
        exit 1
    fi
    if ! psql "postgresql://${VERIFY_USER}@localhost:5432/postgres" -qtAX -c "CREATE DATABASE ${VERIFY_DB};" 2>&1; then
        echo "ERROR: failed to create database ${VERIFY_DB}."
        exit 1
    fi
    echo "Database ${VERIFY_DB} reset"
}

# --- Summary ---

print_summary() {
    echo ""
    echo "========================================="
    printf "Results: ${GREEN}%d passed${NC}, ${RED}%d failed${NC}, ${YELLOW}%d skipped${NC}\n" "$PASS_COUNT" "$FAIL_COUNT" "$SKIP_COUNT"
    echo "========================================="
    if [ "$FAIL_COUNT" -gt 0 ]; then
        exit 1
    fi
}

# --- Cleanup trap ---

cleanup() {
    stop_server
    rm -f "${VERIFY_DIR}/.resp_body.$$"
    rm -f "${VERIFY_DIR}/dsn-test.toml" "${VERIFY_DIR}/dedup-test.toml" "${VERIFY_DIR}/notarget-test.toml"
}
trap cleanup EXIT
