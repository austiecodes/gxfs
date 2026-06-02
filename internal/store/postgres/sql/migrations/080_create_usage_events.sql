create table if not exists {{.UsageEventsTable}} (
    id uuid primary key default gen_random_uuid(),
    created_at timestamptz not null default now(),
    log_id text,
    session_id text,
    client_repo text,
    command text not null,
    exit_code integer not null,
    duration_ms bigint not null,
    event_kind text not null default 'cli.command',
    payload jsonb not null default '{}'::jsonb
);

create index if not exists idx_usage_events_created_at on {{.UsageEventsTable}} (created_at);
create index if not exists idx_usage_events_log_id on {{.UsageEventsTable}} (log_id) where log_id is not null;
create index if not exists idx_usage_events_session_id on {{.UsageEventsTable}} (session_id) where session_id is not null;
create index if not exists idx_usage_events_client_repo on {{.UsageEventsTable}} (client_repo) where client_repo is not null;
create index if not exists idx_usage_events_command on {{.UsageEventsTable}} (command);
