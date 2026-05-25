# `gxfs-server gc run` Is Not an Executable Command

## Problem

`gxfs-server gc --help` displays usage as `gxfs-server gc run [flags]`, which
suggests running `gxfs-server gc run`. That invocation fails with `unknown
command "run" for "gxfs-server gc"`.

## Cause

The Cobra command is registered as the `gc` subcommand, but its `Use` string
is `gc run`. In this shape, `run` appears in generated help while it is not a
registered nested subcommand and the command accepts no positional arguments.

## Solution

Use the actual executable form:

```bash
gxfs-server gc --dry-run
gxfs-server gc --force
```

If the intended public interface is `gxfs-server gc run`, implement `run` as a
real child command; otherwise change the Cobra `Use` text to `gc`.
