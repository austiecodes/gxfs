# GXFS VFS CLI Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first GXFS slice: an agent-facing CLI that talks to a backend server whose storage adapters expose Unix-like VFS capabilities.

**Architecture:** The CLI reads repo-local config and calls `gxfs-server` over HTTP. The server owns storage selection, tree construction, and command behavior. Backend adapters implement capability interfaces (`Lister`, `Treer`, `Catter`, `Grepper`, `Finder`, `Statter`) composed into `store.Adapter`.

**Tech Stack:** Go 1.25.9, Cobra for CLI, `go-zero` for server, `pgxpool` for Postgres, TOML config, table-driven Go tests.

---

## File Structure

- `go.mod`: Go module definition.
- `internal/store/store.go`: Capability interfaces, request/response structs, and common node/match types.
- `internal/vfs/tree.go`: In-memory tree builder and operations.
- `internal/vfs/tree_test.go`: TDD coverage for tree behavior.
- `internal/config/config.go`: CLI and server config loading.
- `internal/config/config_test.go`: Config parsing and environment expansion tests.
- `internal/client/client.go`: HTTP client used by the CLI.
- `internal/client/client_test.go`: Fake-server CLI client tests.
- `cmd/gxfs/main.go`: Cobra CLI entrypoint.
- `cmd/gxfs/main_test.go`: CLI output tests using an injected client.
- `cmd/gxfs-server/main.go`: Server entrypoint.
- `internal/server/server.go`: HTTP routes and adapter dispatch.
- `internal/server/server_test.go`: HTTP handler tests with fake adapters.
- `internal/store/memory/adapter.go`: Test/development adapter backed by `internal/vfs`.
- `internal/store/memory/adapter_test.go`: Adapter interface assertion and behavior tests.
- `internal/store/postgres/adapter.go`: Postgres adapter skeleton and connection handling.
- `internal/store/postgres/adapter_test.go`: Interface assertion and SQL query translation tests.
- `docs/agents/gxfs.md`: Agent usage instructions for `AGENTS.md` / `CLAUDE.md` injection.

## Part 1: Store Capability Interfaces And Module Baseline

**Files:**
- Create: `go.mod`
- Create: `internal/store/store_test.go`
- Create: `internal/store/store.go`

- [x] **Step 1: Write the failing capability-interface test**

```go
package store_test

import (
	"context"
	"testing"

	"gxfs/internal/store"
)

type fakeAdapter struct{}

func (fakeAdapter) LS(context.Context, store.LSRequest) (*store.LSResponse, error) {
	return &store.LSResponse{}, nil
}
func (fakeAdapter) Tree(context.Context, store.TreeRequest) (*store.TreeResponse, error) {
	return &store.TreeResponse{}, nil
}
func (fakeAdapter) Cat(context.Context, store.CatRequest) (*store.CatResponse, error) {
	return &store.CatResponse{}, nil
}
func (fakeAdapter) Grep(context.Context, store.GrepRequest) (*store.GrepResponse, error) {
	return &store.GrepResponse{}, nil
}
func (fakeAdapter) Find(context.Context, store.FindRequest) (*store.FindResponse, error) {
	return &store.FindResponse{}, nil
}
func (fakeAdapter) Stat(context.Context, store.StatRequest) (*store.StatResponse, error) {
	return &store.StatResponse{}, nil
}

var _ store.Adapter = fakeAdapter{}

func TestCapabilityRequestTypesCarryRepoAndPath(t *testing.T) {
	req := store.LSRequest{Repo: "gxfs", Path: "/docs"}
	if req.Repo != "gxfs" || req.Path != "/docs" {
		t.Fatalf("LSRequest = %+v, want repo and path preserved", req)
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store`

Expected: FAIL because `gxfs/internal/store` does not exist.

- [x] **Step 3: Add minimal module and store interface implementation**

Create `go.mod`:

```go
module gxfs

go 1.25.9
```

Create `internal/store/store.go` with:

```go
package store

import "context"

type Node struct {
	Path    string            `json:"path"`
	Name    string            `json:"name"`
	Kind    string            `json:"kind"`
	Size    int64             `json:"size,omitempty"`
	ModTime string            `json:"mod_time,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type LSRequest struct {
	Repo string
	Path string
}
type LSResponse struct {
	Nodes []Node `json:"nodes"`
}

type TreeRequest struct {
	Repo  string
	Path  string
	Depth int
}
type TreeResponse struct {
	Root Node   `json:"root"`
	Text string `json:"text"`
}

type CatRequest struct {
	Repo string
	Path string
}
type CatResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type GrepRequest struct {
	Repo    string
	Path    string
	Pattern string
	Regex   bool
}
type GrepResponse struct {
	Matches []Match `json:"matches"`
}

type FindRequest struct {
	Repo string
	Path string
	Name string
}
type FindResponse struct {
	Nodes []Node `json:"nodes"`
}

type StatRequest struct {
	Repo string
	Path string
}
type StatResponse struct {
	Node Node `json:"node"`
}

type Lister interface {
	LS(context.Context, LSRequest) (*LSResponse, error)
}
type Treer interface {
	Tree(context.Context, TreeRequest) (*TreeResponse, error)
}
type Catter interface {
	Cat(context.Context, CatRequest) (*CatResponse, error)
}
type Grepper interface {
	Grep(context.Context, GrepRequest) (*GrepResponse, error)
}
type Finder interface {
	Find(context.Context, FindRequest) (*FindResponse, error)
}
type Statter interface {
	Stat(context.Context, StatRequest) (*StatResponse, error)
}

type Adapter interface {
	Lister
	Treer
	Catter
	Grepper
	Finder
	Statter
}
```

- [x] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store`

Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add go.mod internal/store
git commit -m "feat: add store capability interfaces"
```

## Part 2: In-Memory VFS Tree

**Files:**
- Create: `internal/vfs/tree_test.go`
- Create: `internal/vfs/tree.go`

- [x] **Step 1: Write failing tests for tree construction and operations**

Tests cover parent directory synthesis, sorted `LS`, `Tree` depth, `Find` glob matching, `Stat`, `Cat`, and `Grep` with plain substring matching.

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/vfs`

Expected: FAIL because package functions are missing.

- [x] **Step 3: Implement minimal tree behavior**

Implement a concrete `Tree` type with `New(files []File) (*Tree, error)`, `LS`, `Tree`, `Cat`, `Grep`, `Find`, and `Stat`.

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/vfs`

Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/vfs
git commit -m "feat: add in-memory vfs tree"
```

## Part 3: Memory Adapter For Server And Tests

**Files:**
- Create: `internal/store/memory/adapter_test.go`
- Create: `internal/store/memory/adapter.go`

- [x] **Step 1: Write failing adapter tests**

Tests assert `var _ store.Adapter = (*Adapter)(nil)` and verify requests delegate to the in-memory tree.

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/memory`

Expected: FAIL because package is missing.

- [x] **Step 3: Implement memory adapter**

Implement `Adapter` as a small concrete struct containing a `*vfs.Tree`.

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/memory`

Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/store/memory
git commit -m "feat: add memory store adapter"
```

## Part 4: Config Loading

**Files:**
- Create: `internal/config/config_test.go`
- Create: `internal/config/config.go`

- [ ] **Step 1: Write failing tests for CLI and server config**

Tests verify CLI config rejects backend credentials, server config accepts backend credentials, and environment variables expand.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config`

Expected: FAIL because package is missing.

- [ ] **Step 3: Implement config loading**

Use a TOML parser. Keep CLI config and server config as separate concrete structs.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/config
git commit -m "feat: add gxfs config loading"
```

## Part 5: HTTP Client And CLI

**Files:**
- Create: `internal/client/client_test.go`
- Create: `internal/client/client.go`
- Create: `cmd/gxfs/main_test.go`
- Create: `cmd/gxfs/main.go`

- [ ] **Step 1: Write failing HTTP client tests**

Use `httptest.Server` to verify `LS`, `Cat`, `Grep`, `Find`, `Tree`, and `Stat` URLs and response decoding.

- [ ] **Step 2: Run client tests to verify they fail**

Run: `go test ./internal/client`

Expected: FAIL because package is missing.

- [ ] **Step 3: Implement minimal HTTP client**

Implement concrete client methods that call `/v1/repos/{repo}/...`.

- [ ] **Step 4: Write failing CLI output tests**

Test command output formatting with an injected fake client.

- [ ] **Step 5: Implement Cobra CLI**

Implement `gxfs ls/tree/cat/grep/find/stat/config doctor` and useful `--help`.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/client ./cmd/gxfs`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/client cmd/gxfs
git commit -m "feat: add gxfs cli client"
```

## Part 6: Server Routes

**Files:**
- Create: `internal/server/server_test.go`
- Create: `internal/server/server.go`
- Create: `cmd/gxfs-server/main.go`

- [ ] **Step 1: Write failing server route tests**

Use a fake `store.Adapter` and assert each route builds the correct request.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server`

Expected: FAIL because package is missing.

- [ ] **Step 3: Implement server routing**

Use `go-zero` for the service entrypoint and keep route handlers thin.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server ./cmd/gxfs-server`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/server cmd/gxfs-server
git commit -m "feat: add gxfs server routes"
```

## Part 7: Postgres Adapter

**Files:**
- Create: `internal/store/postgres/adapter_test.go`
- Create: `internal/store/postgres/adapter.go`
- Create: `internal/store/postgres/query.go`

- [ ] **Step 1: Write failing Postgres adapter tests**

Tests assert `var _ store.Adapter = (*Adapter)(nil)` and verify SQL query construction for file-table mode.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/postgres`

Expected: FAIL because package is missing.

- [ ] **Step 3: Implement adapter skeleton and query builder**

Use `pgxpool` for connection ownership and map result rows into VFS files.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/postgres`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/store/postgres
git commit -m "feat: add postgres store adapter"
```

## Part 8: Agent Docs And Full Verification

**Files:**
- Create: `docs/agents/gxfs.md`

- [ ] **Step 1: Write agent usage document**

Document `gxfs --help`, `gxfs tree / -L 2`, `gxfs ls`, `gxfs cat`, `gxfs grep`, and `gxfs find`.

- [ ] **Step 2: Run full verification**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add docs/agents
git commit -m "docs: add gxfs agent instructions"
```
