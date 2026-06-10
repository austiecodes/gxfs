# Docsets

Docsets are optional and advanced. Prefer `docs://` namespaces and mounts for reusable documentation trees.

Use docsets only when the server explicitly enables curated document sets: named groups of selected docs that may come from several repositories.

## Commands

```bash
rolio docset create best-practices --description "Reusable guidance"
rolio docset add best-practices /go/errors.md --source repo://shared-docs/go/errors.md
rolio docset show best-practices
rolio cat docset://best-practices/go/errors.md
rolio mount add docset://best-practices docs/best-practices
rolio docset rm best-practices /go/errors.md
```

Docset mounts are read-only views of the curated member tree. Use `rolio docset add` and `rolio docset rm` to change membership.

Do not use docsets as a substitute for a writable reusable `docs://` namespace or for mounting a whole repository docs tree.
