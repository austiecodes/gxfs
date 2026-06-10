<!-- ROLIO_START -->
## ROLIO

Use `rolio` for shared docs through Unix-like commands first. Common examples:

- `rolio ls {{ .DocsPath }}`
- `rolio tree {{ .DocsPath }} -L 3`
- `rolio cat {{ .DocsPath }}/foo.md`
- `rolio grep "pattern" {{ .DocsPath }}`
- `rolio find {{ .DocsPath }} --name "*.md"`

For discovery, mounts, sync, writes, hooks, or other ROLIO-specific workflows, load the ROLIO skill at `~/.claude/skills/rolio-skill/SKILL.md` and read only the referenced scenario file you need. If it is absent, generate it with `rolio init --mode skill`.
<!-- ROLIO_END -->
