create table if not exists {{.ContentTable}} (
    {{.PathColumn}} text primary key references {{.NodesTable}}({{.PathColumn}}) on delete cascade,
    content text not null default ''
)
