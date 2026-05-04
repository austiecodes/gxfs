# PostgreSQL timestamptz e2e output drift

## 问题

真实 PostgreSQL e2e 中，`stat --terse` 的 `updated_at` 输出在本机显示为 `+08:00`，而测试期望是稳定的 UTC `Z` 时间。

## 原因

`pgx` 将 `timestamptz` scan 到 `time.Time` 后，会带上当前进程的本地时区。直接 `Format(time.RFC3339)` 会让 CLI 输出依赖运行机器时区。

## 解法

Postgres adapter 在写入 VFS `ModTime` 前先转 UTC：

```go
file.ModTime = mtime.UTC().Format(time.RFC3339)
```

真实数据库 e2e 里保留 `stat --terse` 断言，防止这个输出再次漂移。

## 相关测试注意事项

Docker Postgres 刚启动时，`pg_isready` 可能已经返回 accepting connections，但测试目标数据库还没完成初始化。e2e readiness 要用目标数据库上的实际 SQL 验证：

```bash
docker exec <container> psql -U gxfs -d gxfs -v ON_ERROR_STOP=1 -c "select 1"
```

这样可以避免 seed 阶段偶发撞上 `database "gxfs" does not exist`。
