# GXFS CLI Command Surface Refactor

Status: active refactor plan with product direction and reviewed target model

Last reviewed: 2026-05-31

Sources:

- `cmd/gxfs/main.go`
- `go run ./cmd/gxfs --help`

## Intent

GXFS was intended to feel like a Unix-like command set for virtual filesystem
content. This matters because AI agents already have strong priors for Unix
commands. The more GXFS aligns with familiar commands such as `ls`, `cat`,
`grep`, `find`, `locate`, `stat`, and `rm`, the less custom training or
prompting an agent needs.

The important mental model is:

```text
gxfs ls
gxfs cat
gxfs grep
gxfs locate
gxfs find
```

The CLI also exposes setup, sync, mount, repository, hook, and collection
workflows. Those workflows may still be valid, but they make the command surface
feel broader than a compact filesystem tool when they are all top-level verbs.
Non-Unix commands should either be grouped behind a small number of nouns or
taught explicitly through generated agent context.

This document is an inventory and refactor discussion draft. It records the
current product direction and calls out the remaining open decisions.

## Design Principles

1. Prefer Unix names and semantics where they fit.
2. Keep common VFS inspection commands directly callable at the top level.
3. Treat non-Unix commands as extra cognitive load for agents.
4. Group operational workflows instead of exposing every operation as a
   top-level command.
5. Teach non-Unix workflows through both generated `AGENTS.md` instructions and
   generated skills.

The goal is not to remove every GXFS-specific operation. The goal is to make
the top-level CLI look like a filesystem first, then make the remaining
GXFS-specific behavior explicit and learnable.

## Reviewed Target Model

GXFS should separate four concepts:

| Concept | URI shape | Meaning |
| --- | --- | --- |
| Repo namespace | `repo://<repo>` | A project/repository VFS namespace, usually with its own `/docs` |
| Shared docs namespace | `docs://<name>` | A reusable documentation tree that can be mounted into one or many repos |
| Curated docset | `docset://<name>` | Optional future concept for a selected set of documents, not necessarily a tree |
| Mount entry | `<source-uri> -> <target-path>` | A mapping that makes a source appear as normal paths in the current repo |

The important storage-level decision is that shared documentation such as
`openai-go-sdk` can be a first-class top-level entity. It does not have to be
owned by a consuming repo. A repo can reference it through a mount:

```text
source: docs://openai-go-sdk
target in repo://gxfs: /docs/openai-go-sdk
```

After mounting, agents should not need to know the storage distinction during
normal reading. They continue to use Unix-like commands:

```text
gxfs ls /docs/openai-go-sdk
gxfs cat /docs/openai-go-sdk/usage.md
gxfs grep "streaming" /docs/openai-go-sdk
```

## Current Top-Level Count

`gxfs --help` currently shows 20 top-level commands:

- 18 GXFS business commands registered by the project.
- 2 Cobra-provided commands: `help` and `completion`.

There is also one conditionally registered business command, `collection`, when
the root command is created with an HTTP client adapter. That brings the
business command design surface to 19 commands.

## Current Commands

| Command | Current purpose | Initial bucket | Refactor question |
| --- | --- | --- | --- |
| `ls` | List VFS directory contents | Core Unix-like VFS | Keep top-level |
| `tree` | Print a VFS tree | Core Unix-like VFS | Keep top-level |
| `cat` | Print VFS file content | Core Unix-like VFS | Keep top-level |
| `grep` | Search VFS file content | Core Unix-like VFS | Keep top-level |
| `find` | Find VFS files by name | Core Unix-like VFS | Keep top-level, possibly absorb `glob` |
| `stat` | Print VFS node metadata | Core Unix-like VFS | Keep top-level |
| `locate` | Locate documents by lexical search | Core discovery | Keep top-level if distinct from `find` and `search` |
| `glob` | Find file paths by glob pattern | Discovery overlap | Consider folding into `find -name` or `find -glob` |
| `search` | Search documents by keyword | Discovery overlap | Clarify difference from `grep` and `locate` |
| `write` | Write content to a VFS file | Mutation | Keep if GXFS should be writable from Unix-like verbs |
| `edit` | Replace text in a VFS file | Mutation | Keep if simple patching is a first-class CLI operation |
| `rm` | Delete a VFS file or directory | Mutation | Keep top-level Unix-like mutation command |
| `init` | Initialize `.gxfs` config in a repo | Setup and agent guidance | Keep top-level; extend with agent context generation modes |
| `config` | Inspect GXFS CLI configuration | Setup and diagnostics | Keep top-level group |
| `repo` | Repository management commands | Management | Keep as grouped command |
| `mount` | Manage mount points | Management | Keep as grouped command |
| `sync` | Synchronize local docs with GXFS | Sync group | Keep as grouped command |
| `hook` | GXFS lifecycle hooks | Automation/internal | Consider hiding or moving under an internal/admin group |
| `collection` | Collection management commands | Product workflow | Re-evaluate name and surface; this overlaps with shared docs/docset behavior |
| `help` | Help about any command | Cobra built-in | Keep implicit |
| `completion` | Generate shell completion scripts | Cobra built-in | Keep implicit or hide from primary docs |

## Possible Target Shape

The top-level command surface could be split into three tiers.

### Tier 1: Primary Unix-Like Commands

These should remain easy to discover and directly callable:

```text
gxfs ls
gxfs tree
gxfs cat
gxfs grep
gxfs find
gxfs locate
gxfs stat
```

Writable Unix-like commands:

```text
gxfs write
gxfs edit
gxfs rm
```

### Tier 2: Grouped Operational Commands

These can stay public but should be grouped so they do not compete with the
filesystem verbs:

```text
gxfs config ...
gxfs repo ...
gxfs mount ...
gxfs sync ...
```

Completed moves in this refactor:

```text
gxfs attach              -> gxfs mount attach
gxfs refresh             -> gxfs sync refresh
gxfs materialize         -> gxfs sync materialize
gxfs dematerialize       -> gxfs sync dematerialize
gxfs delete              -> gxfs rm
```

`init`, `config`, `repo`, `mount`, and `sync` can remain as the small set of
GXFS-specific nouns. They should be documented as operational commands, not as
part of the Unix-like inspection surface.

`repo` should be a discovery and inspection command, not the place where shared
docs are attached. The current minimum useful shape is:

```text
gxfs repo ls
gxfs repo info <repo>
gxfs repo current
```

`repo ls` is the repo discovery command: it tells the agent what repo
namespaces the server knows about. It should not mutate mounts or docs.

`mount` should be the command that makes one repo's docs visible inside another
repo's VFS view:

```text
gxfs mount ls
gxfs mount sources
gxfs mount add repo://openai-go-sdk/docs /docs/openai-go-sdk
gxfs mount add docs://openai-go-sdk /docs/openai-go-sdk
gxfs mount rm /docs/openai-go-sdk
gxfs mount attach openai-go-sdk --into /docs/openai-go-sdk
```

This keeps the user-facing model close to Unix: repos are namespaces, mounts
compose filesystems, and the agent still uses `gxfs ls`, `gxfs cat`, and
`gxfs grep` after the mount exists.

### Tier 3: Advanced or Internal Commands

These may be hidden from normal help, moved into a namespace, or documented as
advanced features:

```text
gxfs hook ...
gxfs collection ...
gxfs completion
```

`completion` is standard Cobra behavior, so hiding it is optional. `hook` and
`collection` need product-level decisions.

## Repo, Shared Docs, and Collections

The intended model is:

- Every repo has a VFS namespace and normally exposes its docs under `/docs`.
- A repo may reuse docs from another repo or shared docs source.
- Shared docs should appear in the consuming repo as normal paths so agents can
  keep using Unix-like commands.

Example:

```text
gxfs mount add repo://openai-go-sdk/docs /docs/openai-go-sdk
gxfs ls /docs/openai-go-sdk
gxfs grep "Responses API" /docs/openai-go-sdk
```

This model makes `mount` the natural user-facing command for shared docs.
`repo` is only for discovering and inspecting available repo namespaces.

### Shared Docs as Top-Level Entities

A reusable docs area such as `openai-go-sdk` should be able to exist as its own
top-level storage entity. A repo can then mount it into its own `/docs` tree:

```text
source: docs://openai-go-sdk
target in repo://gxfs: /docs/openai-go-sdk
```

or, if the shared docs are represented as a repo namespace:

```text
source: repo://openai-go-sdk
target in repo://gxfs: /docs/openai-go-sdk
```

The user-facing workflow should still be mount-based:

```text
gxfs mount add docs://openai-go-sdk /docs/openai-go-sdk
gxfs mount add repo://openai-go-sdk /docs/openai-go-sdk
```

The recommended URI scheme for this case is `docs://openai-go-sdk`. `folder://`
is too generic: it does not tell an agent whether the source is a plain
directory, a shared docs namespace, a repo, or a curated list. `docset://` should
be reserved for the narrower case where GXFS needs a curated set of selected
documents rather than a reusable docs tree.

Recommended direction:

- Use `repo://...` for real repository namespaces.
- Use `docs://...` for reusable documentation trees that are not themselves
  code repos.
- Reserve `docset://...` for curated document sets if that concept remains
  useful.
- Let `gxfs repo ls` discover repo namespaces.
- Let `gxfs mount sources` discover mountable sources, including
  `repo://...`, `docs://...`, and possibly `docset://...`.

Implementation can still start with compatibility adapters, but the target
model should not treat shared docs as fake repos. The storage layer should have
enough namespace metadata to distinguish repo namespaces from shared docs
namespaces.

The current `collection` implementation is different. It represents a named,
curated set of document references across repos, with each member pointing at a
stored document ID and an internal collection path. That is useful, but it is
not the same as "repo A references repo B's docs directory."

Because of that mismatch, `collection` needs a naming decision:

- Hide `collection` as an internal backend concept if shared docs are expressed
  entirely through `mount`.
- Rename it to a more explicit public concept such as `docset` if curated
  reusable doc groups remain first-class.
- Keep `collection` only as a compatibility alias if existing users depend on
  it.

For AI ergonomics, `docset` is clearer than `collection` if the object means
"a reusable set of docs." `collection` is too generic and does not communicate
whether it is a repo, a path, a mounted tree, or a curated list.

## Agent Guidance Generation

Some commands are necessary but not naturally inferable from Unix knowledge:

```text
gxfs init
gxfs config
gxfs repo
gxfs mount
gxfs mount sources
gxfs sync
gxfs sync refresh
gxfs sync materialize
gxfs sync dematerialize
gxfs hook
gxfs docs/docset if introduced
gxfs search
```

For these commands, GXFS should generate explicit agent-facing instructions.
Two output forms should be retained:

- Markdown instructions for repository context, such as `AGENTS.md`.
- A local skill that teaches the agent GXFS-specific workflows.

Implemented command shape:

```text
gxfs init --mode md
gxfs init --mode skill
gxfs init --mode md,skill
```

Users can choose either output form or generate both. The generated content
explains only the GXFS-specific commands and assumes the agent already
understands Unix-like commands.

## Non-Unix Command Consolidation

| Former/current command | Target | Reason |
| --- | --- | --- |
| `init` | `init` | Keep as the entrypoint for local setup and generated agent guidance |
| `config` | `config` | Keep as a grouped diagnostic/config command |
| `repo` | `repo` | Keep as repo discovery and namespace inspection |
| `mount` | `mount` | Keep as the way to reference shared docs inside a repo |
| `attach` | `mount attach` | Attaching is a mount workflow |
| `sync` | `sync` | Keep as a grouped sync command |
| `refresh` | `sync refresh` | Refreshing the manifest is a sync workflow |
| `materialize` | `sync materialize` | Materialization is part of local sync/state management |
| `dematerialize` | `sync dematerialize` | Dematerialization is part of local sync/state management |
| `hook` | hidden or `admin hook` | Lifecycle hooks are not a normal filesystem operation |
| `collection` | hide or migrate to `docset` | Current collection semantics are curated doc references, not repo docs mounts or shared docs namespaces |
| `search` | possibly `locate` mode or advanced search | It overlaps with `grep` and `locate` and needs a sharper meaning |

## Discovery Command Overlap

The most confusing part of the current surface is the number of search-like
verbs:

```text
gxfs grep
gxfs find
gxfs glob
gxfs locate
gxfs search
```

Initial distinction to validate:

- `grep`: search file content, Unix-compatible mental model.
- `find`: search paths/names in a tree.
- `glob`: path pattern search; likely belongs inside `find`.
- `locate`: global document lookup, potentially cross-repository.
- `search`: keyword document search; overlaps with `grep` and `locate`.

Open question: should GXFS expose both `locate` and `search`, or should one be
implemented as flags/modes on the other?

## Breaking Migration Notes

This project is still in rapid iteration, so the current refactor intentionally
removes old compatibility entrypoints instead of keeping aliases:

- `gxfs delete` is removed; use `gxfs rm`.
- `gxfs attach` is removed; use `gxfs mount attach`.
- `gxfs refresh` is removed; use `gxfs sync refresh`.
- `gxfs materialize` is removed; use `gxfs sync materialize`.
- `gxfs dematerialize` is removed; use `gxfs sync dematerialize`.
- `gxfs repo list` is removed; use `gxfs repo ls`.
- `gxfs mount list` and `gxfs mount remove` are removed; use `gxfs mount ls`
  and `gxfs mount rm`.

Primary docs and generated agent instructions should teach only the new shape.

## Open Decisions

1. Should writable commands be part of the core Unix-like surface, or should
   read-only browsing stay the primary identity?
2. Should `glob` become `find -glob` or `find -name`?
3. Should `search` and `locate` both survive as top-level commands?
4. Should `collection` be hidden or renamed to `docset`?
5. Should advanced commands be hidden from default help?
6. What metadata and storage tables are needed for first-class `docs://...`
   namespaces?
7. Does curated `docset://...` remain necessary after first-class `docs://...`
   namespaces exist?
