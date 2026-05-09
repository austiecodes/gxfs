<!-- GXFS_START -->
## GXFS

Use gxfs CLI to browse and update this project's shared internal documentation. Treat gxfs like a virtual `{{ .DocsPath }}` directory; prefer it over scanning local files when you need project docs.

- `gxfs ls {{ .DocsPath }}` - list docs
- `gxfs tree {{ .DocsPath }} -L 3` - inspect doc structure
- `gxfs cat {{ .DocsPath }}/foo.md` - read a doc
- `gxfs grep "pattern" {{ .DocsPath }}` - search docs
- `gxfs find / --name "*.md"` - find by name
- `gxfs stat {{ .DocsPath }}/foo.md` - file metadata
- `gxfs write {{ .DocsPath }}/foo.md "content"` - create or overwrite a doc
- `gxfs edit {{ .DocsPath }}/foo.md --old "text" --new "text"` - update text in a doc
- `gxfs delete {{ .DocsPath }}/foo.md` - delete a doc

The docs root defaults to `{{ .DocsPath }}` and can be changed in `.gxfs/settings.toml` under `[docs].path`.
<!-- GXFS_END -->
