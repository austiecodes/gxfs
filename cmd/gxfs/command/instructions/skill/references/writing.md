# Writing Docs

Use this when changing GXFS-backed documents. Confirm the target path is writable before editing shared docs.

## Create or Replace

```bash
gxfs write {{ .DocsPath }}/new.md "# New Doc"
cat local.md | gxfs write {{ .DocsPath }}/local.md
```

`write` creates parent directories as needed. If content is omitted, stdin is used.

## Edit Existing Content

```bash
gxfs edit {{ .DocsPath }}/new.md --old "New" --new "Updated"
gxfs edit {{ .DocsPath }}/new.md --old "foo" --new "bar" --all
```

Prefer exact `--old` strings. Use `--all` only when every occurrence should change.

## Delete

```bash
gxfs rm {{ .DocsPath }}/new.md
gxfs rm {{ .DocsPath }}/old-section
```

Directory deletes are recursive. Inspect with `gxfs tree` or `gxfs find` first when the path is broad.

## Writable Mounts

Cross-repository writes require a writable mount and server-side permission:

```bash
gxfs mount add repo://shared-docs/docs docs/shared --mode writable
gxfs edit docs/shared/guide.md --old "old" --new "new"
```

If GXFS reports stale local state, run `gxfs sync refresh` or `gxfs sync pull`, inspect the latest content, and retry.
