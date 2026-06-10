# Discovery and Remote Preview

Use this when the target docs may not already be mounted into the current view.
Discovery commands can look beyond `{{ .DocsPath }}` and return remote refs.

## Repository Search

```bash
rolio search "migration rollback"
rolio search "migration rollback" --path {{ .DocsPath }}
rolio search "migration rollback" --json
```

`search` is ranked text search for document discovery. Use `--path` when the search should stay within a known subtree.

## Cross-Repo Lookup

```bash
rolio locate "openai client"
rolio locate "openai client" --all-repos
rolio locate "openai client" --json
```

`locate` returns `repo://` references that can be read directly or mounted.

## Path Pattern Discovery

```bash
rolio glob "**/*.md"
rolio glob "**/*.md" --all-repos
rolio glob "docs/**/*.go" --long
```

Use `glob` when the path shape matters more than content.

## Source Discovery

```bash
rolio repo ls
rolio mount sources
```

`repo ls` lists known repository namespaces. `mount sources` lists mountable `repo://` and `docs://` sources.

## Remote Preview Without Mounting

```bash
rolio cat repo://shared-docs/docs/guide.md
rolio cat repo://other-repo/docs/foo.md
```

Preview remote docs before adding a mount when you only need one file or want to confirm the source.
