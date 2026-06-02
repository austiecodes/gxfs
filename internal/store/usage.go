package store

import (
	"context"
	"encoding/json"
)

const UsageEventKindCLICommand = "cli.command"

type UsageEvent struct {
	ID         string          `json:"id,omitempty"`
	CreatedAt  string          `json:"created_at,omitempty"`
	LogID      string          `json:"log_id,omitempty"`
	SessionID  string          `json:"session_id,omitempty"`
	ClientRepo string          `json:"client_repo,omitempty"`
	Command    string          `json:"command"`
	ExitCode   int             `json:"exit_code"`
	DurationMs int64           `json:"duration_ms"`
	EventKind  string          `json:"event_kind,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type UsageEventResponse struct {
	Event UsageEvent `json:"event"`
}

type UsageRecorder interface {
	RecordUsageEvent(context.Context, UsageEvent) (*UsageEventResponse, error)
}
