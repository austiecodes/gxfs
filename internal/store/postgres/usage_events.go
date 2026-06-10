package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/austiecodes/rolio/internal/store"
)

var (
	_ store.UsageRecorder = (*Adapter)(nil)
	_ store.UsageRecorder = (*DocAdapter)(nil)
	_ store.UsageRecorder = (*Registry)(nil)
)

func (a *Adapter) RecordUsageEvent(ctx context.Context, event store.UsageEvent) (*store.UsageEventResponse, error) {
	return recordUsageEvent(ctx, a.pool, a.cfg, event)
}

func (d *DocAdapter) RecordUsageEvent(ctx context.Context, event store.UsageEvent) (*store.UsageEventResponse, error) {
	return recordUsageEvent(ctx, d.pool, d.cfg, event)
}

func (r *Registry) RecordUsageEvent(ctx context.Context, event store.UsageEvent) (*store.UsageEventResponse, error) {
	return recordUsageEvent(ctx, r.pool, r.cfg, event)
}

func recordUsageEvent(ctx context.Context, pool *pgxpool.Pool, cfg Config, event store.UsageEvent) (*store.UsageEventResponse, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: usage event storage is unavailable", store.ErrNotSupported)
	}
	if event.Command == "" {
		return nil, fmt.Errorf("%w: command is required", store.ErrInvalidParam)
	}
	if event.EventKind == "" {
		event.EventKind = store.UsageEventKindCLICommand
	}
	payload := event.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("%w: payload must be valid JSON", store.ErrInvalidParam)
	}

	table, err := quoteTable(cfg.Schema, "rolio_usage_events")
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(
		`insert into %s (log_id, session_id, client_repo, command, exit_code, duration_ms, event_kind, payload)
values ($1, $2, $3, $4, $5, $6, $7, $8)
returning id::text, created_at`,
		table,
	)

	var createdAt time.Time
	if err := pool.QueryRow(ctx, query,
		nullString(event.LogID),
		nullString(event.SessionID),
		nullString(event.ClientRepo),
		event.Command,
		event.ExitCode,
		event.DurationMs,
		event.EventKind,
		string(payload),
	).Scan(&event.ID, &createdAt); err != nil {
		return nil, fmt.Errorf("record usage event: %w", err)
	}
	event.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	event.Payload = payload
	return &store.UsageEventResponse{Event: event}, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
