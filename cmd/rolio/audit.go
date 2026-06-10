package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var auditMu sync.Mutex

type auditEntry struct {
	Timestamp  string `json:"timestamp"`
	LogID      string `json:"log_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Command    string `json:"command"`
	DurationMs int64  `json:"duration_ms"`
	ExitCode   int    `json:"exit_code"`
}

// auditPath returns the audit JSONL file path:
//   - .rolio/audit.jsonl if .rolio/ exists in cwd
//   - ~/.rolio/audit.jsonl as fallback
//   - "" if neither is available (audit disabled)
func auditPath() string {
	rolioDir := ".rolio"
	if fi, err := os.Stat(rolioDir); err == nil && fi.IsDir() {
		return filepath.Join(rolioDir, "audit.jsonl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	globalDir := filepath.Join(home, ".rolio")
	return filepath.Join(globalDir, "audit.jsonl")
}

func appendAudit(logID, sessionID, command string, durationMs int64, exitCode int) {
	if os.Getenv("ROLIO_AUDIT") == "0" {
		return
	}

	path := auditPath()
	if path == "" {
		return
	}

	entry := auditEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		LogID:      logID,
		SessionID:  sessionID,
		Command:    command,
		DurationMs: durationMs,
		ExitCode:   exitCode,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	auditMu.Lock()
	defer auditMu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "%s\n", data)
}
