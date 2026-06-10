---
name: rolio
description: Use ROLIO to browse, search, mount, synchronize, and update shared repository documentation with Unix-like commands.
---

# ROLIO

Use ROLIO as a virtual filesystem for shared docs. Start with the Unix-like surface:

- `rolio ls {{ .DocsPath }}` lists files and directories.
- `rolio cat {{ .DocsPath }}/foo.md` reads a file.
- `rolio grep "pattern" {{ .DocsPath }}` searches file contents.
- `rolio find {{ .DocsPath }} --name "*.md"` finds paths by name.
- `rolio stat {{ .DocsPath }}/foo.md` inspects metadata.
- `rolio tree {{ .DocsPath }} -L 3` previews the directory shape.

Read only the reference file for the workflow you need:

- [Browsing mounted docs](references/browse.md): `ls`, `tree`, `cat`, `grep`, `find`, and `stat`.
- [Discovery and remote preview](references/discovery.md): `search`, `locate`, `glob`, `repo ls`, `mount sources`, and direct `repo://` reads.
- [Mounting shared docs](references/mounting.md): `repo://` and `docs://` mounts, mount modes, attach, list, and remove.
- [Sync and materialization](references/sync.md): refresh manifests, materialize or dematerialize files, push/pull local docs.
- [Writing docs](references/writing.md): `write`, `edit`, `rm`, writable mounts, and conflict-safe workflow.
- [Setup, hooks, and operations](references/setup-hooks-ops.md): `init`, config files, agent hooks, server config, and GC.
- [Docsets](references/docsets.md): optional curated document sets when the server enables them.

Do not use removed compatibility aliases such as top-level `delete`, `refresh`, `materialize`, `dematerialize`, or `attach`. Use `rm`, `sync ...`, and `mount attach` instead.
