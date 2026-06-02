# Docsets

Docsets are optional and advanced. Prefer `docs://` namespaces and mounts for reusable documentation trees.

Use docsets only when the server explicitly enables curated document sets: named groups of selected docs that may come from several repositories.

## Commands

```bash
gxfs docset create best-practices --description "Reusable guidance"
gxfs docset add best-practices /go/errors.md --source repo://shared-docs/go/errors.md
gxfs docset show best-practices
gxfs cat docset://best-practices/go/errors.md
gxfs docset rm best-practices /go/errors.md
```

Do not use docsets as a substitute for mounting a whole repository docs tree or a reusable `docs://` namespace.
