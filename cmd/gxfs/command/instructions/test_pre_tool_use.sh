#!/bin/bash
# Tests for pre_tool_use.sh hook script.
# Run: bash instructions/test_pre_tool_use.sh

set -euo pipefail

SCRIPT="cmd/gxfs/command/instructions/pre_tool_use.sh"
PASS=0
FAIL=0

assert_valid_json() {
    local desc="$1" output="$2"
    if echo "$output" | python3 -m json.tool >/dev/null 2>&1; then
        PASS=$((PASS + 1))
    else
        echo "FAIL: $desc — invalid JSON output: $output"
        FAIL=$((FAIL + 1))
    fi
}

assert_no_updated_input() {
    local desc="$1" output="$2"
    if echo "$output" | python3 -c "
import sys, json
d = json.load(sys.stdin)
if 'updatedInput' in d.get('hookSpecificOutput', {}):
    sys.exit(1)
" 2>/dev/null; then
        PASS=$((PASS + 1))
    else
        echo "FAIL: $desc — should not have updatedInput: $output"
        FAIL=$((FAIL + 1))
    fi
}

assert_has_updated_input() {
    local desc="$1" output="$2" expected_prefix="$3"
    actual=$(echo "$output" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(d['hookSpecificOutput']['updatedInput']['command'])
" 2>/dev/null)
    if [ -n "$actual" ] && echo "$actual" | grep -q "^$expected_prefix"; then
        PASS=$((PASS + 1))
    else
        echo "FAIL: $desc — expected updatedInput starting with '$expected_prefix', got: $actual"
        FAIL=$((FAIL + 1))
    fi
}

# Test 1: Simple gxfs command is rewritten with GXFS_LOG_ID prefix.
output=$(printf '%s' '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"gxfs locate auth --all-repos"}}' | bash "$SCRIPT" 2>/dev/null)
assert_valid_json "simple gxfs command produces valid JSON" "$output"
assert_has_updated_input "simple gxfs command is rewritten" "$output" "GXFS_LOG_ID="

# Test 2: Quoted command (gxfs write with quoted content) produces valid JSON.
output=$(printf '%s' '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"gxfs write docs/a.md \"hello world\""}}' | bash "$SCRIPT" 2>/dev/null)
assert_valid_json "quoted command produces valid JSON" "$output"
assert_has_updated_input "quoted command is rewritten" "$output" "GXFS_LOG_ID="

# Test 3: Non-gxfs command is allowed without updatedInput.
output=$(printf '%s' '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls -la"}}' | bash "$SCRIPT" 2>/dev/null)
assert_valid_json "non-gxfs command produces valid JSON" "$output"
assert_no_updated_input "non-gxfs command is not rewritten" "$output"

# Test 4: Complex command with pipe is not rewritten.
output=$(printf '%s' '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"gxfs locate auth | grep -i token"}}' | bash "$SCRIPT" 2>/dev/null)
assert_valid_json "pipe command produces valid JSON" "$output"
assert_no_updated_input "pipe command is not rewritten" "$output"

# Test 5: Complex command with && is not rewritten.
output=$(printf '%s' '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"cd repo && gxfs ls /docs"}}' | bash "$SCRIPT" 2>/dev/null)
assert_valid_json "&& command produces valid JSON" "$output"
assert_no_updated_input "&& command is not rewritten" "$output"

# Test 6: Non-Bash tool is allowed without updatedInput.
output=$(printf '%s' '{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"main.go"}}' | bash "$SCRIPT" 2>/dev/null)
assert_valid_json "non-Bash tool produces valid JSON" "$output"
assert_no_updated_input "non-Bash tool is not rewritten" "$output"

# Test 7: Codex-shaped PreToolUse input is rewritten.
output=$(printf '%s' '{"session_id":"s1","hook_event_name":"PreToolUse","turn_id":"t1","cwd":"/tmp/repo","tool_name":"Bash","tool_input":{"command":"gxfs locate auth --all-repos"}}' | bash "$SCRIPT" 2>/dev/null)
assert_valid_json "Codex-shaped input produces valid JSON" "$output"
assert_has_updated_input "Codex-shaped input is rewritten" "$output" "GXFS_LOG_ID="

# Test 8: Hook audit log does not contain raw command text.
tmpdir=$(mktemp -d)
trap "rm -rf $tmpdir" EXIT
mkdir -p "$tmpdir/.gxfs"
(
    cd "$tmpdir"
    printf '%s' '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"gxfs write docs/a.md \"secret content here\""}}' | bash "$OLDPWD/$SCRIPT" >/dev/null 2>&1 || true
)
if [ -f "$tmpdir/.gxfs/audit.jsonl" ]; then
    if grep -q "secret content" "$tmpdir/.gxfs/audit.jsonl"; then
        echo "FAIL: hook audit log contains raw command text"
        FAIL=$((FAIL + 1))
    else
        PASS=$((PASS + 1))
    fi
    # Verify command_name field exists.
    if grep -q '"command_name":"write"' "$tmpdir/.gxfs/audit.jsonl"; then
        PASS=$((PASS + 1))
    else
        echo "FAIL: hook audit missing command_name field"
        FAIL=$((FAIL + 1))
    fi
else
    echo "FAIL: hook audit log was not created"
    FAIL=$((FAIL + 1))
fi

echo ""
echo "Results: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
