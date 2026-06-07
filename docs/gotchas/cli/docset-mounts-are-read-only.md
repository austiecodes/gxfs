# Docset mounts are read-only member views

## Problem

Docsets can be mounted with `gxfs mount add docset://... <local-path>`, but
the mounted content must stay read-only. Treating `docset://...` like a
writable `docs://...` namespace would blur two different APIs.

## Cause

Docsets are curated membership lists. `gxfs docset add` and `gxfs docset rm`
change which existing documents belong to the set, while document content is
owned by the source repo/docs bindings behind each `gxfs_docs` row.

The docset source adapter therefore exposes docset members as a virtual
readable tree and returns `store.ErrReadOnlyMount` for `Put`, `Edit`, and
`Delete`.

## Solution

Use docset mounts for browsing curated selected docs:

```bash
gxfs docset create best-practices
gxfs docset add best-practices /guide.md --source repo://shared-docs/guide.md
gxfs mount add docset://best-practices docs/best-practices
gxfs tree docs/best-practices
gxfs cat docs/best-practices/guide.md
```

Use docset commands to change membership:

```bash
gxfs docset add best-practices /new.md --source repo://shared-docs/new.md
gxfs docset rm best-practices /guide.md
```

Use `docs://...` namespaces when the goal is a writable reusable documentation
tree.
