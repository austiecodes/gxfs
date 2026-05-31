-- DB-backed repository registry.
create table if not exists {{.ReposTable}} (
    name text primary key,
    writable bool not null default false,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    check (name <> '')
);

-- Preserve existing repo namespaces discovered from document path bindings.
insert into {{.ReposTable}} (name)
select distinct repo
from {{.RepoPathsTable}}
where repo <> ''
on conflict (name) do nothing;
