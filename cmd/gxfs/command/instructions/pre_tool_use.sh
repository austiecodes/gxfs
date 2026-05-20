#!/bin/bash
# gxfs PreToolUse hook — intercepts gxfs commands and injects GXFS_LOG_ID
# for three-layer audit correlation (hook → CLI → server).
#
# Installed by: gxfs init --claude-hooks=project
# Format: Claude Code PreToolUse hook JSON on stdin, JSON on stdout.

set -euo pipefail

# Read hook input from stdin.
input=$(cat)

# Extract fields from hook JSON using python3 for reliable parsing.
tool_name=$(echo "$input" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(d.get('tool_name', ''))
" 2>/dev/null || echo "")

command=$(echo "$input" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(d.get('tool_input', {}).get('command', ''))
" 2>/dev/null || echo "")

session_id=$(echo "$input" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(d.get('session_id', ''))
" 2>/dev/null || echo "")

# Only intercept Bash tool.
if [ "$tool_name" != "Bash" ]; then
    echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}'
    exit 0
fi

# Conservative match: only simple "gxfs ..." or "/path/to/gxfs ..." forms.
# Skip if command contains pipes, &&, ||, or semicolons.
if echo "$command" | grep -qE '[|&;]'; then
    echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}'
    exit 0
fi

# Extract the first word (the executable).
first_word=$(echo "$command" | awk '{print $1}')

# Check if first word is gxfs or ends with /gxfs.
is_gxfs=false
basename_first=$(basename "$first_word" 2>/dev/null || echo "")
if [ "$basename_first" = "gxfs" ]; then
    is_gxfs=true
fi

if [ "$is_gxfs" = "false" ]; then
    echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}'
    exit 0
fi

# Generate log ID.
log_id=$(uuidgen 2>/dev/null || python3 -c "import uuid; print(uuid.uuid4())" 2>/dev/null || echo "")

if [ -z "$log_id" ]; then
    echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}'
    exit 0
fi

# Extract command_name (second word, i.e. the gxfs subcommand).
command_name=$(echo "$command" | awk '{print $2}')

# Write pre-tool-call hook log (minimal fields only, no raw args).
hook_log_dir=""
if [ -d ".gxfs" ]; then
    hook_log_dir=".gxfs"
elif [ -d "$HOME/.gxfs" ]; then
    hook_log_dir="$HOME/.gxfs"
fi

if [ -n "$hook_log_dir" ]; then
    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    python3 -c "
import json, sys
entry = {
    'timestamp': sys.argv[1],
    'log_id': sys.argv[2],
    'session_id': sys.argv[3],
    'hook': 'pre_tool_use',
    'command_name': sys.argv[4],
}
with open(sys.argv[5], 'a') as f:
    f.write(json.dumps(entry, separators=(',', ':')) + '\n')
" "$timestamp" "$log_id" "$session_id" "$command_name" "$hook_log_dir/audit.jsonl" 2>/dev/null || true
fi

# Output: rewrite command with GXFS_LOG_ID env prefix.
# Use python3 for reliable JSON encoding.
rewritten="GXFS_LOG_ID=$log_id $command"

python3 -c "
import json, sys
resp = {
    'hookSpecificOutput': {
        'hookEventName': 'PreToolUse',
        'permissionDecision': 'allow',
        'updatedInput': {
            'command': sys.argv[1]
        }
    }
}
print(json.dumps(resp))
" "$rewritten"
