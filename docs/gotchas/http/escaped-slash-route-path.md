# Escaped Slashes in Repo Route Parameters

## Problem

Repo names such as `austiecodes/xxxx` can be sent as `/v1/repos/austiecodes%2Fxxxx/ls`, but handlers that parse `r.URL.Path` may see the slash decoded and reject the route as malformed.

## Cause

Go's `net/http` URL parsing exposes a decoded `URL.Path`. A `%2F` inside a route parameter can become `/`, so splitting the path on `/` no longer preserves the original segment boundary.

## Solution

Parse route parameters from `r.URL.EscapedPath()` when encoded slashes are valid inside a path segment, then `url.PathUnescape` only the specific parameter segment after splitting.

If a router matches routes before the handler runs, verify that the router can dispatch the decoded path shape too. For go-zero's path router, `/v1/repos/austiecodes%2Fxxxx/ls` is matched as `/v1/repos/austiecodes/xxxx/ls`, so GXFS registers an extra two-segment route variant and lets the handler recover the original name from `EscapedPath()`.
