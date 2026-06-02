# Mounting Shared Docs

Use this when shared docs should behave like normal local GXFS paths in the current repo.

## Mental Model

- `repo://<repo>/<path>` mounts docs from another repository namespace.
- `docs://<name>/<path>` mounts reusable documentation that is not owned by a consuming repo.
- The mount target is a local path such as `docs/shared` or `docs/libs/openai-go`.

## Add Mounts

```bash
gxfs mount sources
gxfs mount add repo://shared-docs/docs docs/shared
gxfs mount add docs://openai-go-sdk/reference docs/openai-go-sdk
gxfs mount add repo://shared-docs/docs docs/shared --mode writable
```

Mounts default to `readonly`. Use `--mode writable` only when writes should flow through the mount and the source repo permits it.

## Inspect and Remove

```bash
gxfs mount ls
gxfs mount rm docs/shared
```

Remove or change materialized mounts only after checking local files and sync state.

## Attach by Keyword

```bash
gxfs mount attach openai-go --into docs/libs/openai-go
gxfs mount attach openai-go --into docs/libs/openai-go --dry-run
```

Use `mount attach` when a human-friendly keyword should resolve to one repository. If multiple repos match, inspect `gxfs repo ls` and retry with a more exact name.

## After Mounting

Once mounted, use normal read commands:

```bash
gxfs tree docs/openai-go-sdk -L 3
gxfs cat docs/openai-go-sdk/usage.md
gxfs grep "streaming" docs/openai-go-sdk
```
