<!-- GXFS_START -->
## GXFS

Use `gxfs` for shared docs through Unix-like commands first. Common examples:

- `gxfs ls {{ .DocsPath }}`
- `gxfs tree {{ .DocsPath }} -L 3`
- `gxfs cat {{ .DocsPath }}/foo.md`
- `gxfs grep "pattern" {{ .DocsPath }}`
- `gxfs find {{ .DocsPath }} --name "*.md"`

For discovery, mounts, sync, writes, hooks, or other GXFS-specific workflows, load the GXFS skill at `~/.claude/skills/gxfs-skill/SKILL.md` and read only the referenced scenario file you need. If it is absent, generate it with `gxfs init --mode skill`.
<!-- GXFS_END -->
