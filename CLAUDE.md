# AGENTS

# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 文档索引

- [实现计划](docs/superpowers/plans/2026-05-03-gxfs-vfs-cli-server.md) — 分步实现 plan（含 checkbox 跟踪）
- [设计规格](docs/superpowers/specs/2026-05-03-gxfs-vfs-cli-server-design.md) — 架构决策、接口定义、测试策略
- `docs/gotchas/` — 踩坑记录。按 topic 建子目录（如 `testing/`、`go-zero/`），每个坑点一个 md，格式：问题 → 原因 → 解法

## PostgreSQL (Docker)

```bash
docker exec -it gxfs-pg psql -U gxfs -d gxfs                  # 连接
docker start gxfs-pg                                            # 启动
docker stop gxfs-pg                                             # 停止
docker exec gxfs-pg psql -U gxfs -d gxfs -c "SELECT count(*) FROM vfs_files"
```

DSN: `postgres://gxfs:gxfs@localhost:5432/gxfs?sslmode=disable`

## Build & Test

```bash
go test ./...                              # 运行所有测试
go test ./internal/store                   # 单个 package 测试
go test ./internal/vfs -run TestGrep       # 运行单个测试
go build ./cmd/gxfs                        # 构建 CLI
go build ./cmd/gxfs-server                 # 构建 server
```

## 架构概览

GXFS 是面向 agent 的虚拟文件系统，由 thin CLI + backend server 组成。

```
gxfs CLI  ──HTTP──>  gxfs-server  ──>  store.Adapter
                                          ├─ memory   (测试/开发)
                                          └─ postgres (生产，pgxpool)
```

- **CLI** (`cmd/gxfs`) — Cobra 命令行，读 `.gxfs/settings.toml` 配置，通过 HTTP client 调 server。不感知存储细节。
- **Server** (`cmd/gxfs-server`) — go-zero HTTP 服务，加载 `conf/server.toml`，持有 store adapter，提供 `/v1/repos/{repo}/{op}` API。
- **Store 边界** — `internal/store/store.go` 定义 capability interface（Lister/Treer/Catter/Grepper/Finder/Statter/Writer），组合为 `store.Adapter`。每个 adapter 必须包含 `var _ store.Adapter = (*Adapter)(nil)` 编译期断言。
- **VFS Tree** (`internal/vfs/tree.go`) — 内存树，自动合成父目录，提供 LS/Tree/Cat/Grep/Find/Stat 操作。
- **Client** (`internal/client/client.go`) — HTTP client，实现 `store.Adapter`，URL 格式为 `/v1/repos/{repo}/{op}?path=...`。
- **Config** (`internal/config/config.go`) — TOML 配置，CLI config 禁止包含 backend 凭证，server config 持有存储连接信息。环境变量自动展开。

## 关键约定

- 接口只在真正的多态边界定义（store adapter），其余用 concrete struct。
- Postgres adapter 惰性构建 vfs tree：元数据（`vfs_nodes`）一次性加载，content（`vfs_content`）按需延迟加载并缓存。
- DB schema：`vfs_nodes`(path PK) + `vfs_content`(path PK) + `vfs_repo_nodes`(repo, path) 多对多映射，doc 存一份，多 repo 共享。
- `grep` 默认纯文本子串匹配，`-E` 开启正则。
- CLI 不直连数据库，所有操作经 server HTTP API。
