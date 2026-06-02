# Discovery and Remote Preview

Use this when the target docs may not already be mounted into the current view.
Discovery commands can look beyond `{{ .DocsPath }}` and return remote refs.

## Repository Search

```bash
gxfs search "migration rollback"
gxfs search "migration rollback" --path {{ .DocsPath }}
gxfs search "migration rollback" --json
```

`search` is ranked text search for document discovery. Use `--path` when the search should stay within a known subtree.

## Cross-Repo Lookup

```bash
gxfs locate "openai client"
gxfs locate "openai client" --all-repos
gxfs locate "openai client" --json
```

`locate` returns `repo://` references that can be read directly or mounted.

## Path Pattern Discovery

```bash
gxfs glob "**/*.md"
gxfs glob "**/*.md" --all-repos
gxfs glob "docs/**/*.go" --long
```

Use `glob` when the path shape matters more than content.

## Source Discovery

```bash
gxfs repo ls
gxfs mount sources
```

`repo ls` lists known repository namespaces. `mount sources` lists mountable `repo://` and `docs://` sources.

## Remote Preview Without Mounting

```bash
gxfs cat repo://shared-docs/docs/guide.md
gxfs cat repo://other-repo/docs/foo.md
```

Preview remote docs before adding a mount when you only need one file or want to confirm the source.
