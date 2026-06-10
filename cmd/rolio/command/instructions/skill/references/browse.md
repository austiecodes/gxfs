# Browsing Mounted Docs

Use this when the needed docs are already visible under the mounted ROLIO view.
ROLIO intentionally mirrors common Unix file inspection commands.

## Start

```bash
rolio ls {{ .DocsPath }}
rolio tree {{ .DocsPath }} -L 3
rolio cat {{ .DocsPath }}/guide.md
```

## List and Inspect

```bash
rolio ls -la {{ .DocsPath }}
rolio tree {{ .DocsPath }} -L 3
rolio stat {{ .DocsPath }}/guide.md
rolio stat -f {{ .DocsPath }}/guide.md
```

## Read Content

```bash
rolio cat {{ .DocsPath }}/guide.md
rolio cat -n {{ .DocsPath }}/guide.md
rolio cat -b {{ .DocsPath }}/guide.md
```

## Search Mounted Content

```bash
rolio grep "database" {{ .DocsPath }}
rolio grep -i "database" {{ .DocsPath }}
rolio grep -E "db|database" {{ .DocsPath }}
rolio grep -C 2 "migration" {{ .DocsPath }}
rolio grep --include "*.md" --exclude "archive/*" "token" {{ .DocsPath }}
rolio grep -l "TODO" {{ .DocsPath }}
rolio grep -c "TODO" {{ .DocsPath }}
```

`grep` uses plain substring matching by default. Use `-E` only when regex mode is needed.

## Find Paths

```bash
rolio find {{ .DocsPath }} --name "*.md"
rolio find {{ .DocsPath }} --iname "*readme*"
rolio find {{ .DocsPath }} --type f --maxdepth 3 --name "*.md"
rolio find {{ .DocsPath }} --type d --name "api"
```
