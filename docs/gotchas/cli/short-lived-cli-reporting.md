# Short-Lived CLI Reporting

## Problem

A CLI command needs to write a local audit record and also report a usage event
to the server without changing the user's command result.

## Cause

GXFS CLI invocations are short-lived processes. If the command starts a
fire-and-forget goroutine for server reporting and then returns from `main`,
the Go runtime exits the process and the goroutine may be killed before the
HTTP request is sent.

## Solution

Write the local audit record first, then attempt server reporting with a short
`context.WithTimeout`. Ignore reporting failures so the original command exit
code is preserved. Use a local audit file as the durable fallback and add a
future flush/retry command if guaranteed delivery becomes necessary.
