---
name: gxfs
description: Use GXFS to browse, search, mount, synchronize, and update shared repository documentation with Unix-like commands.
---

# GXFS

Use GXFS as a virtual filesystem for shared docs. Prefer the Unix-aligned commands first:

- `gxfs ls {{ .DocsPath }}` lists files and directories.
- `gxfs cat {{ .DocsPath }}/foo.md` reads a file.
- `gxfs grep "pattern" {{ .DocsPath }}` searches file contents. Use `-E` for regex.
- `gxfs find {{ .DocsPath }} --name "*.md"` finds paths by name.
- `gxfs stat {{ .DocsPath }}/foo.md` inspects metadata.
- `gxfs tree {{ .DocsPath }} -L 3` previews the directory shape.
- `gxfs rm {{ .DocsPath }}/foo.md` deletes a file.

For discovery beyond the mounted view:

- `gxfs search "query"` performs semantic or full-text search across the repo.
- `gxfs glob "**/*.md"` matches virtual paths.
- `gxfs repo ls` lists available repositories.
- `gxfs mount sources` lists mountable `repo://` and `docs://` sources.

Mount reusable docs explicitly when the answer should draw from shared knowledge:

- `gxfs mount add repo://other-repo/docs libs/other-repo`
- `gxfs mount add docs://openai-go-sdk/reference libs/openai-go`
- `gxfs mount ls`
- `gxfs mount rm libs/openai-go`
- `gxfs mount attach openai-go --into libs/openai-go`

Use `docs://<name>/<path>` for top-level reusable docs namespaces. A namespace such as `docs://openai-go-sdk/reference` can be mounted into many repos without copying content.

Use `sync` subcommands when local markdown files need to match GXFS state:

- `gxfs sync refresh {{ .DocsPath }}` updates the local manifest.
- `gxfs sync materialize {{ .DocsPath }}` writes GXFS docs into local markdown files.
- `gxfs sync dematerialize {{ .DocsPath }} --keep-files` marks files remote-only while keeping local files.

For writes, prefer explicit commands:

- `gxfs write {{ .DocsPath }}/foo.md "content"`
- `gxfs edit {{ .DocsPath }}/foo.md --old "x" --new "y"`
- `gxfs rm {{ .DocsPath }}/foo.md`

Do not use removed compatibility aliases such as top-level `delete`, `refresh`, `materialize`, `dematerialize`, or `attach`. Use `rm`, `sync ...`, and `mount attach` instead.
