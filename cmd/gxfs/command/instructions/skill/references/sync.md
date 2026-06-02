# Sync and Materialization

Use this when local markdown files need to match GXFS state, or when edits should be compared with remote hashes.

## Refresh Metadata

```bash
gxfs sync refresh {{ .DocsPath }}
gxfs sync refresh {{ .DocsPath }} --manifest .gxfs/manifest.toml
```

`refresh` updates the local manifest without necessarily writing every remote file to disk.

## Pull Remote Docs

```bash
gxfs sync pull {{ .DocsPath }}
gxfs sync pull {{ .DocsPath }} --materialize
```

Use `pull` when remote docs should be reflected locally. Add `--materialize` when local files should be written.

## Push Local Docs

```bash
gxfs sync push {{ .DocsPath }}
gxfs sync push {{ .DocsPath }} --manifest .gxfs/manifest.toml
```

Use `push` to upload local docs into GXFS. Check the command output for conflicts before assuming remote state changed.

## Materialize or Dematerialize

```bash
gxfs sync materialize {{ .DocsPath }}
gxfs sync dematerialize {{ .DocsPath }}
gxfs sync dematerialize {{ .DocsPath }} --keep-files
```

`materialize` writes GXFS docs into local files. `dematerialize --keep-files` marks files remote-only while preserving existing local files.

## Conflict-Safe Pattern

Before writing to a path with local files, refresh or pull first:

```bash
gxfs sync refresh {{ .DocsPath }}
gxfs cat {{ .DocsPath }}/target.md
gxfs edit {{ .DocsPath }}/target.md --old "old" --new "new"
```
