# Repo Names Do Not Belong in HTTP Path Segments

## Problem

Repo names such as `github.com/austiecodes/xxxx` contain `/`, so putting the repo name inside the HTTP path makes the path ambiguous. The slash is both part of the repo name and the URL path separator.

One failure mode is double encoding: if code writes `url.PathEscape(repo)` into `url.URL.Path`, `URL.String()` escapes the `%` bytes again. The server then sees encoded text after one decode instead of the canonical repo name.

## Cause

Go's `net/http` URL parsing exposes a decoded `URL.Path`. A `%2F` inside a route parameter can become `/`, so splitting the path on `/` no longer preserves the original segment boundary.

On the client, `url.URL.Path` must contain the decoded path. If it contains already escaped text, `URL.String()` treats `%` as a literal path character and emits `%25`.

## Solution

Keep repo and docs namespace names out of the route path. Use a stable operation route plus a query field, for example `/v1/repos/ls?repo=...&path=...` and `/v1/docs/cat?name=...&path=...`.

When constructing outbound URLs with `net/url`, put the operation in `URL.Path` and the repo or docs namespace name in `RawQuery` through `url.Values`.
