<!-- GXFS_START -->
## GXFS

Use gxfs CLI to browse and update shared docs. Commands align with Unix: `ls`, `cat`, `grep`, `find`, `stat`, `tree` work as expected on virtual paths under `{{ .DocsPath }}`.

**Browsing** (Unix-aligned, work on mounted view):
- `gxfs ls {{ .DocsPath }}` / `gxfs tree {{ .DocsPath }} -L 3`
- `gxfs cat {{ .DocsPath }}/foo.md`
- `gxfs grep "pattern" {{ .DocsPath }}`
- `gxfs find {{ .DocsPath }} --name "*.md"`
- `gxfs stat {{ .DocsPath }}/foo.md`

**Discovery** (bypass mount, search whole repo):
- `gxfs search "query"` — full-text search across entire repo
- `gxfs glob "**/*.md"` — path pattern match
- `gxfs glob "**/*.md" --all-repos` — search across all repos
- `gxfs repo ls` — list available repos
- `gxfs mount sources` — list mountable `repo://` and `docs://` sources

**Remote preview** (read without mounting):
- `gxfs cat repo://other-repo/docs/foo.md`

**Mounting** (shared docs):
- `gxfs mount add repo://other-repo/docs libs/other-repo` — mount remote docs (readonly)
- `gxfs mount add docs://openai-go-sdk/reference libs/openai-go` — mount reusable docs namespace
- `gxfs mount ls` — show current mounts
- `gxfs mount attach <keyword> --into libs/<name>` — discover + auto-mount by keyword

**Writing**:
- `gxfs write {{ .DocsPath }}/foo.md "content"` — create or overwrite
- `gxfs edit {{ .DocsPath }}/foo.md --old "x" --new "y"` — string replacement
- `gxfs rm {{ .DocsPath }}/foo.md` — delete a doc

Config: `.gxfs/settings.toml`. Mounts: `.gxfs/mounts.toml`. Use `gxfs tree {{ .DocsPath }} -L 3` to start.
<!-- GXFS_END -->
