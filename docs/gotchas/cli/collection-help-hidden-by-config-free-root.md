# `collection` help can disappear in config-free help mode

## Problem

`gxfs collection --help` can report:

```text
unknown command "collection" for "gxfs"
```

The root `gxfs --help` output can also omit `collection`, even though the
runtime root command may register it.

## Cause

Help requests take the config-free path in `cmd/gxfs/main.go`. That path builds
the root command with nil adapters:

```go
cmd := newRootCommand(nil, nil, "", nil)
```

`collection` is conditionally registered only when `rawAdapter` is a
`*client.Client`, so the config-free help command tree does not include it.

## Solution

When changing the CLI command surface, decide whether `collection` is public.

If it is public, register its command shape independently of runtime adapter
availability so help can resolve it. Runtime execution can still fail later if
the configured adapter cannot support collections.

If it is not public, keep it hidden or move it under an advanced/admin command
group and document that decision.
