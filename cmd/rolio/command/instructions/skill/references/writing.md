# Writing Docs

Use this when changing ROLIO-backed documents. Confirm the target path is writable before editing shared docs.

## Create or Replace

```bash
rolio write {{ .DocsPath }}/new.md "# New Doc"
cat local.md | rolio write {{ .DocsPath }}/local.md
```

`write` creates parent directories as needed. If content is omitted, stdin is used.

## Edit Existing Content

```bash
rolio edit {{ .DocsPath }}/new.md --old "New" --new "Updated"
rolio edit {{ .DocsPath }}/new.md --old "foo" --new "bar" --all
```

Prefer exact `--old` strings. Use `--all` only when every occurrence should change.

## Delete

```bash
rolio rm {{ .DocsPath }}/new.md
rolio rm {{ .DocsPath }}/old-section
```

Directory deletes are recursive. Inspect with `rolio tree` or `rolio find` first when the path is broad.

## Writable Mounts

Cross-repository writes require a writable mount and server-side permission:

```bash
rolio mount add repo://shared-docs/docs docs/shared --mode writable
rolio edit docs/shared/guide.md --old "old" --new "new"
```

If ROLIO reports stale local state, run `rolio sync refresh` or `rolio sync pull`, inspect the latest content, and retry.
