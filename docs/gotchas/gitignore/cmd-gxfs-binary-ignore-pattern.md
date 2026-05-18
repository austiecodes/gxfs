# Problem

New files created under `cmd/gxfs/` do not appear in `git status`, even though they exist on disk and are part of the build.

# Cause

The root `.gitignore` used the bare pattern `gxfs` to ignore the compiled CLI binary. In Git ignore syntax, that also matches any path segment named `gxfs`, so it unintentionally ignored new files anywhere under `cmd/gxfs/`.

# Solution

Anchor the binary ignore rules to the repository root:

- use `/gxfs`
- use `/gxfs-server`

This keeps the built binaries ignored without hiding new source files under `cmd/gxfs/`.
