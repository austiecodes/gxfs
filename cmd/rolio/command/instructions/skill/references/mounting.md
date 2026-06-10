# Mounting Shared Docs

Use this when shared docs should behave like normal local ROLIO paths in the current repo.

## Mental Model

- `repo://<repo>/<path>` mounts docs from another repository namespace.
- `docs://<name>/<path>` mounts reusable documentation that is not owned by a consuming repo.
- The mount target is a local path such as `docs/shared` or `docs/libs/openai-go`.

## Add Mounts

```bash
rolio mount sources
rolio mount add repo://shared-docs/docs docs/shared
rolio mount add docs://openai-go-sdk/reference docs/openai-go-sdk
rolio mount add repo://shared-docs/docs docs/shared --mode writable
```

Mounts default to `readonly`. Use `--mode writable` only when writes should flow through the mount and the source repo permits it.

## Inspect and Remove

```bash
rolio mount ls
rolio mount rm docs/shared
```

Remove or change materialized mounts only after checking local files and sync state.

## Attach by Keyword

```bash
rolio mount attach openai-go --into docs/libs/openai-go
rolio mount attach openai-go --into docs/libs/openai-go --dry-run
```

Use `mount attach` when a human-friendly keyword should resolve to one repository. If multiple repos match, inspect `rolio repo ls` and retry with a more exact name.

## After Mounting

Once mounted, use normal read commands:

```bash
rolio tree docs/openai-go-sdk -L 3
rolio cat docs/openai-go-sdk/usage.md
rolio grep "streaming" docs/openai-go-sdk
```
