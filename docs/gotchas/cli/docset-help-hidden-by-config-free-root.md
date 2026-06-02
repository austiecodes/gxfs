# Public subcommands must exist in config-free help mode

## Problem

Historically `gxfs docset --help` could report:

```text
unknown command "docset" for "gxfs"
```

The root `gxfs --help` output could also omit `docset`, even though the
runtime root command registered it.

## Cause

Help requests take the config-free path in `cmd/gxfs/main.go`. That path builds
the root command with nil adapters:

```go
cmd := newRootCommand(nil, nil, "", nil)
```

`docset` was conditionally registered only when `rawAdapter` was a
`*client.Client`, so the config-free help command tree did not include it.

## Solution

When a command is public, register its shape independently of runtime adapter
availability so `gxfs --help` and `gxfs <command> --help` can always resolve
it. Runtime execution can still fail later with a clear configuration error if
the active adapter does not support that command surface.
