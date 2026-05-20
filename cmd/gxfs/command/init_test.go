package command

import "testing"

func TestUpsertHookGroupIdempotent(t *testing.T) {
	target := map[string]any{"type": "command", "command": "/usr/bin/gxfs hook session-start"}

	groups := []any{}
	groups, changed := upsertHookGroup(groups, "startup|resume", target)
	if !changed {
		t.Error("first upsert should report changed")
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	groups, changed = upsertHookGroup(groups, "startup|resume", target)
	if changed {
		t.Error("second upsert of same command should not report changed")
	}
	if len(groups) != 1 {
		t.Fatalf("expected still 1 group, got %d", len(groups))
	}
}

func TestUpsertHookGroupDifferentMatchers(t *testing.T) {
	target1 := map[string]any{"type": "command", "command": "hook1.sh"}
	target2 := map[string]any{"type": "command", "command": "hook2.sh"}

	groups := []any{}
	groups, _ = upsertHookGroup(groups, "startup|resume", target1)
	groups, _ = upsertHookGroup(groups, "Bash", target2)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
}
