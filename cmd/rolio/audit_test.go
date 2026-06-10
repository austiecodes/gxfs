package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditPathLocalRolioDir(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	if err := os.MkdirAll(".rolio", 0o755); err != nil {
		t.Fatal(err)
	}

	got := auditPath()
	want := filepath.Join(".rolio", "audit.jsonl")
	if got != want {
		t.Errorf("auditPath() = %q, want %q", got, want)
	}
}

func TestAuditPathGlobalFallback(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	got := auditPath()
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".rolio", "audit.jsonl")
	if got != want {
		t.Errorf("auditPath() = %q, want %q", got, want)
	}
}

func TestAppendAuditWritesJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	if err := os.MkdirAll(".rolio", 0o755); err != nil {
		t.Fatal(err)
	}

	appendAudit("test-log-id-123", "", "locate", 42, 0)

	data, err := os.ReadFile(".rolio/audit.jsonl")
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}

	line := strings.TrimSpace(string(data))
	if !strings.Contains(line, `"log_id":"test-log-id-123"`) {
		t.Errorf("audit line missing log_id: %s", line)
	}
	if !strings.Contains(line, `"command":"locate"`) {
		t.Errorf("audit line missing command: %s", line)
	}
	if !strings.Contains(line, `"duration_ms":42`) {
		t.Errorf("audit line missing duration_ms: %s", line)
	}
	if !strings.Contains(line, `"exit_code":0`) {
		t.Errorf("audit line missing exit_code: %s", line)
	}
}

func TestAppendAuditDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	if err := os.MkdirAll(".rolio", 0o755); err != nil {
		t.Fatal(err)
	}

	os.Setenv("ROLIO_AUDIT", "0")
	defer os.Unsetenv("ROLIO_AUDIT")

	appendAudit("should-not-appear", "", "cat", 10, 0)

	if _, err := os.Stat(".rolio/audit.jsonl"); !os.IsNotExist(err) {
		t.Error("audit file should not exist when ROLIO_AUDIT=0")
	}
}

func TestAppendAuditNoLogID(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	if err := os.MkdirAll(".rolio", 0o755); err != nil {
		t.Fatal(err)
	}

	appendAudit("", "", "ls", 5, 0)

	data, err := os.ReadFile(".rolio/audit.jsonl")
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}

	line := strings.TrimSpace(string(data))
	if strings.Contains(line, "log_id") {
		t.Errorf("empty log_id should be omitted: %s", line)
	}
}

func TestAppendAuditWriteFailureSilent(t *testing.T) {
	appendAudit("test", "", "cat", 1, 0)
}
