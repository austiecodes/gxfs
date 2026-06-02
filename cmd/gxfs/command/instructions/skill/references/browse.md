# Browsing Mounted Docs

Use this when the needed docs are already visible under the mounted GXFS view.
GXFS intentionally mirrors common Unix file inspection commands.

## Start

```bash
gxfs ls {{ .DocsPath }}
gxfs tree {{ .DocsPath }} -L 3
gxfs cat {{ .DocsPath }}/guide.md
```

## List and Inspect

```bash
gxfs ls -la {{ .DocsPath }}
gxfs tree {{ .DocsPath }} -L 3
gxfs stat {{ .DocsPath }}/guide.md
gxfs stat -f {{ .DocsPath }}/guide.md
```

## Read Content

```bash
gxfs cat {{ .DocsPath }}/guide.md
gxfs cat -n {{ .DocsPath }}/guide.md
gxfs cat -b {{ .DocsPath }}/guide.md
```

## Search Mounted Content

```bash
gxfs grep "database" {{ .DocsPath }}
gxfs grep -i "database" {{ .DocsPath }}
gxfs grep -E "db|database" {{ .DocsPath }}
gxfs grep -C 2 "migration" {{ .DocsPath }}
gxfs grep --include "*.md" --exclude "archive/*" "token" {{ .DocsPath }}
gxfs grep -l "TODO" {{ .DocsPath }}
gxfs grep -c "TODO" {{ .DocsPath }}
```

`grep` uses plain substring matching by default. Use `-E` only when regex mode is needed.

## Find Paths

```bash
gxfs find {{ .DocsPath }} --name "*.md"
gxfs find {{ .DocsPath }} --iname "*readme*"
gxfs find {{ .DocsPath }} --type f --maxdepth 3 --name "*.md"
gxfs find {{ .DocsPath }} --type d --name "api"
```
