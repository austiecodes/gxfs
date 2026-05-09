create table if not exists {{.NodesTable}} (
    {{.PathColumn}} text primary key,
    {{.KindColumn}} text not null default 'file',
    {{.SizeColumn}} bigint not null default 0,
    {{.MTimeColumn}} timestamptz not null default now(),
    check ({{.KindColumn}} in ('file', 'dir'))
)
