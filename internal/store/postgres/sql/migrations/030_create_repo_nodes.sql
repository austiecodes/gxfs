create table if not exists {{.RepoNodesTable}} (
    repo text not null,
    {{.PathColumn}} text not null references {{.NodesTable}}({{.PathColumn}}) on delete cascade,
    primary key (repo, {{.PathColumn}})
)
