# Sync and Materialization

Use this when local markdown files need to match ROLIO state, or when edits should be compared with remote hashes.

## Refresh Metadata

```bash
rolio sync refresh {{ .DocsPath }}
rolio sync refresh {{ .DocsPath }} --manifest .rolio/manifest.toml
```

`refresh` updates the local manifest without necessarily writing every remote file to disk.

## Pull Remote Docs

```bash
rolio sync pull {{ .DocsPath }}
rolio sync pull {{ .DocsPath }} --materialize
```

Use `pull` when remote docs should be reflected locally. Add `--materialize` when local files should be written.

## Push Local Docs

```bash
rolio sync push {{ .DocsPath }}
rolio sync push {{ .DocsPath }} --manifest .rolio/manifest.toml
```

Use `push` to upload local docs into ROLIO. Check the command output for conflicts before assuming remote state changed.

## Materialize or Dematerialize

```bash
rolio sync materialize {{ .DocsPath }}
rolio sync dematerialize {{ .DocsPath }}
rolio sync dematerialize {{ .DocsPath }} --keep-files
```

`materialize` writes ROLIO docs into local files. `dematerialize --keep-files` marks files remote-only while preserving existing local files.

## Conflict-Safe Pattern

Before writing to a path with local files, refresh or pull first:

```bash
rolio sync refresh {{ .DocsPath }}
rolio cat {{ .DocsPath }}/target.md
rolio edit {{ .DocsPath }}/target.md --old "old" --new "new"
```
