# GXFS

Use `gxfs` to inspect virtual filesystem content exposed by `gxfs-server`.

Prefer GXFS before direct database access when you need to browse mounted knowledge or reference files.

## Start Here

```bash
gxfs --help
gxfs tree / -L 2
gxfs ls /
```

## Common Commands

```bash
gxfs ls /docs
gxfs tree /docs -L 3
gxfs cat /docs/readme.md
gxfs grep "type Adapter" /go
gxfs grep -E "type .* interface" /go
gxfs find / -name "*.go"
gxfs stat /docs/readme.md
```

## Rules For Agents

- Use `gxfs tree / -L 2` to learn the top-level shape before broad reading.
- Use `gxfs ls <path>` to inspect one directory.
- Use `gxfs cat <path>` only when exact file content is needed.
- Use `gxfs grep <pattern> <path>` before reading many files.
- Use `gxfs find <path> -name <glob>` when you know the filename shape.
- Run `gxfs <command> --help` when arguments are unclear.
- Do not assume the CLI knows storage credentials; storage backend details belong to `gxfs-server`.
