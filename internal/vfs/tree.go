package vfs

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/austiecodes/gxfs/internal/store"
)

const (
	KindDir  = "dir"
	KindFile = "file"
)

type Node = store.Node
type Match = store.Match

type File struct {
	Path    string
	Content string
	Size    int64
	ModTime string
	Meta    map[string]string
}

type Tree struct {
	nodes    map[string]Node
	children map[string][]string
	content  map[string]string
}

func New(files []File) (*Tree, error) {
	tree := &Tree{
		nodes: map[string]Node{
			"/": {Path: "/", Name: "/", Kind: KindDir},
		},
		children: make(map[string][]string),
		content:  make(map[string]string),
	}

	for _, file := range files {
		filePath, err := cleanFilePath(file.Path)
		if err != nil {
			return nil, err
		}
		if _, ok := tree.nodes[filePath]; ok {
			return nil, fmt.Errorf("duplicate path: %s", filePath)
		}

		tree.addParents(path.Dir(filePath))

		size := file.Size
		if size == 0 {
			size = int64(len(file.Content))
		}
		tree.nodes[filePath] = Node{
			Path:    filePath,
			Name:    path.Base(filePath),
			Kind:    KindFile,
			Size:    size,
			ModTime: file.ModTime,
			Meta:    file.Meta,
		}
		tree.children[path.Dir(filePath)] = appendChild(tree.children[path.Dir(filePath)], filePath)
		if file.Content != "" {
			tree.content[filePath] = file.Content
		}
	}

	for parent := range tree.children {
		sort.Slice(tree.children[parent], func(i, j int) bool {
			return tree.nodes[tree.children[parent][i]].Name < tree.nodes[tree.children[parent][j]].Name
		})
	}

	return tree, nil
}

func NewFromNodes(nodes []Node) (*Tree, error) {
	tree := &Tree{
		nodes: map[string]Node{
			"/": {Path: "/", Name: "/", Kind: KindDir},
		},
		children: make(map[string][]string),
		content:  make(map[string]string),
	}

	for _, node := range nodes {
		nodePath := cleanPath(node.Path)
		if nodePath == "/" {
			continue
		}

		switch node.Kind {
		case KindDir, KindFile:
			tree.addParents(path.Dir(nodePath))
			parent := path.Dir(nodePath)
			if !contains(tree.children[parent], nodePath) {
				tree.children[parent] = appendChild(tree.children[parent], nodePath)
			}
		default:
			return nil, fmt.Errorf("unsupported node kind %q for %s", node.Kind, nodePath)
		}

		if node.Name == "" {
			node.Name = path.Base(nodePath)
		}
		node.Path = nodePath
		tree.nodes[nodePath] = node
	}

	for parent := range tree.children {
		sort.Slice(tree.children[parent], func(i, j int) bool {
			return tree.nodes[tree.children[parent][i]].Name < tree.nodes[tree.children[parent][j]].Name
		})
	}

	return tree, nil
}

type LSOptions struct {
	Sort      string
	Reverse   bool
	Recursive bool
	All       bool
}

func (t *Tree) LS(p string, opts LSOptions) ([]Node, error) {
	node, err := t.mustDir(p)
	if err != nil {
		return nil, err
	}

	var nodes []Node
	if opts.Recursive {
		nodes = t.collectRecursive(node.Path, opts.All)
	} else {
		paths := t.children[node.Path]
		nodes = make([]Node, 0, len(paths))
		for _, child := range paths {
			nodes = append(nodes, t.nodes[child])
		}
	}

	if !opts.All {
		nodes = filterHidden(nodes)
	}

	sortNodes(nodes, opts.Sort, opts.Reverse)
	return nodes, nil
}

func (t *Tree) collectRecursive(dir string, showHidden bool) []Node {
	var nodes []Node
	for _, child := range t.children[dir] {
		node := t.nodes[child]
		if !showHidden && isHidden(node.Name) {
			continue
		}
		nodes = append(nodes, node)
		if node.Kind == KindDir {
			nodes = append(nodes, t.collectRecursive(node.Path, showHidden)...)
		}
	}
	return nodes
}

func sortNodes(nodes []Node, sortField string, reverse bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		less, equal := compareField(nodes[i], nodes[j], sortField)
		if equal {
			return false
		}
		if reverse {
			return !less
		}
		return less
	})
}

func SortNodesCopy(nodes []Node, sortField string, reverse bool) []Node {
	cp := make([]Node, len(nodes))
	copy(cp, nodes)
	sortNodes(cp, sortField, reverse)
	return cp
}

func compareField(a, b Node, field string) (less, equal bool) {
	switch field {
	case "size":
		if a.Size == b.Size {
			return false, true
		}
		return a.Size < b.Size, false
	case "mtime":
		if a.ModTime == b.ModTime {
			return false, true
		}
		if at, aok := parseModTime(a.ModTime); aok {
			if bt, bok := parseModTime(b.ModTime); bok {
				if at.Equal(bt) {
					return false, true
				}
				return at.Before(bt), false
			}
		}
		return a.ModTime < b.ModTime, false
	default:
		if a.Name == b.Name {
			return false, true
		}
		return a.Name < b.Name, false
	}
}

func parseModTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

func filterHidden(nodes []Node) []Node {
	filtered := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if !isHidden(n.Name) {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

type TreeOptions struct {
	All       bool
	DirsOnly  bool
	FullPath  bool
	ShowSize  bool
	Sort      string
	DirsFirst bool
}

func (t *Tree) Tree(p string, depth int, opts TreeOptions) (string, error) {
	node, err := t.mustDir(p)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	if opts.FullPath {
		out.WriteString(node.Path)
	} else {
		out.WriteString(node.Name)
	}
	if node.Path == "/" {
		out.Reset()
		out.WriteString("/")
	}
	out.WriteByte('\n')
	t.writeTree(&out, node.Path, 1, depth, opts)
	return out.String(), nil
}

func (t *Tree) Cat(p string) (string, error) {
	filePath := cleanPath(p)
	node, ok := t.nodes[filePath]
	if !ok {
		return "", fmt.Errorf("%w: %s", store.ErrNotFound, filePath)
	}
	if node.Kind == KindDir {
		return "", fmt.Errorf("%w: %s", store.ErrIsDir, filePath)
	}
	if content, loaded := t.content[filePath]; loaded {
		return content, nil
	}
	return "", fmt.Errorf("%w: %s", store.ErrContentNotReady, filePath)
}

func (t *Tree) HasContent(p string) bool {
	filePath := cleanPath(p)
	_, ok := t.content[filePath]
	return ok
}

func (t *Tree) SetContent(p, content string) {
	filePath := cleanPath(p)
	if _, ok := t.nodes[filePath]; ok {
		t.content[filePath] = content
	}
}

type GrepOptions struct {
	CaseInsensitive bool
	Invert          bool
	WholeWord       bool
	WholeLine       bool
	ContextBefore   int
	ContextAfter    int
	All             bool
	Include         string
	Exclude         string
}

func (t *Tree) Grep(root, pattern string, regex bool, opts GrepOptions) ([]Match, error) {
	rootPath := cleanPath(root)
	if _, err := t.mustDir(rootPath); err != nil {
		return nil, err
	}

	effectivePattern := pattern
	if regex {
		if opts.CaseInsensitive {
			effectivePattern = "(?i)" + effectivePattern
		}
		if opts.WholeWord {
			effectivePattern = `\b` + effectivePattern + `\b`
		}
		if opts.WholeLine {
			effectivePattern = `^` + effectivePattern + `$`
		}
	}

	var re *regexp.Regexp
	if regex {
		compiled, err := regexp.Compile(effectivePattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		re = compiled
	}

	paths := t.filePathsUnder(rootPath)
	if !opts.All {
		paths = filterHiddenPaths(paths, rootPath)
	}
	paths = filterByGlob(paths, opts.Include, opts.Exclude)

	var matches []Match
	for _, filePath := range paths {
		lines := strings.Split(t.content[filePath], "\n")
		for i, line := range lines {
			matched := grepLineMatches(line, pattern, re, opts)
			if matched {
				m := Match{
					Path: filePath,
					Line: i + 1,
					Text: line,
				}
				if opts.ContextBefore > 0 {
					start := max(0, i-opts.ContextBefore)
					m.Before = copyLines(lines[start:i])
				}
				if opts.ContextAfter > 0 {
					end := min(len(lines), i+1+opts.ContextAfter)
					m.After = copyLines(lines[i+1 : end])
				}
				matches = append(matches, m)
			}
		}
	}
	return matches, nil
}

type FindOptions struct {
	Type     string
	MaxDepth int
	MinDepth int
	All      bool
	IName    string
}

func (t *Tree) Find(root, name string, opts FindOptions) ([]Node, error) {
	rootPath := cleanPath(root)
	if _, err := t.mustDir(rootPath); err != nil {
		return nil, err
	}

	candidates := t.allNodesUnder(rootPath)

	var nodes []Node
	for _, node := range candidates {
		if !opts.All && pathHasHiddenComponent(node.Path, rootPath) {
			continue
		}

		switch opts.Type {
		case "dir":
			if node.Kind != KindDir {
				continue
			}
		case "file", "":
			if node.Kind != KindFile {
				continue
			}
		}

		depth := relativeDepth(rootPath, node.Path)
		if opts.MaxDepth > 0 && depth > opts.MaxDepth {
			continue
		}
		if opts.MinDepth > 0 && depth < opts.MinDepth {
			continue
		}

		matched := false
		if opts.IName != "" {
			ok, err := path.Match(strings.ToLower(opts.IName), strings.ToLower(node.Name))
			if err != nil {
				return nil, fmt.Errorf("invalid iname pattern: %w", err)
			}
			matched = ok
		} else {
			ok, err := path.Match(name, node.Name)
			if err != nil {
				return nil, fmt.Errorf("invalid name pattern: %w", err)
			}
			matched = ok
		}
		if matched {
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

func (t *Tree) Stat(p string) (Node, error) {
	nodePath := cleanPath(p)
	node, ok := t.nodes[nodePath]
	if !ok {
		return Node{}, fmt.Errorf("%w: %s", store.ErrNotFound, nodePath)
	}
	return node, nil
}

func (t *Tree) Put(p, content string) {
	filePath, err := cleanFilePath(p)
	if err != nil {
		return
	}

	t.addParents(path.Dir(filePath))

	size := int64(len(content))
	now := time.Now().UTC().Format(time.RFC3339)
	t.nodes[filePath] = Node{
		Path:    filePath,
		Name:    path.Base(filePath),
		Kind:    KindFile,
		Size:    size,
		ModTime: now,
	}
	t.content[filePath] = content

	dir := path.Dir(filePath)
	children := t.children[dir]
	if !contains(children, filePath) {
		t.children[dir] = appendChild(children, filePath)
		sort.Slice(t.children[dir], func(i, j int) bool {
			return t.nodes[t.children[dir][i]].Name < t.nodes[t.children[dir][j]].Name
		})
	}
}

func (t *Tree) Delete(p string) error {
	nodePath := cleanPath(p)
	if nodePath == "/" {
		return store.ErrCannotDeleteRoot
	}
	node, ok := t.nodes[nodePath]
	if !ok {
		return fmt.Errorf("%w: %s", store.ErrNotFound, nodePath)
	}

	if node.Kind == KindDir {
		for _, child := range t.children[nodePath] {
			t.deleteRecursive(child)
		}
		delete(t.children, nodePath)
	}
	delete(t.nodes, nodePath)
	delete(t.content, nodePath)

	t.removeFromParent(nodePath)
	return nil
}

func (t *Tree) Edit(p, old, newStr string, all bool) (int, error) {
	if old == "" {
		return 0, store.ErrEmptyOld
	}

	content, err := t.Cat(p)
	if err != nil {
		return 0, err
	}

	var replaced int
	if all {
		replaced = strings.Count(content, old)
		content = strings.ReplaceAll(content, old, newStr)
	} else {
		replaced = strings.Count(content, old)
		if replaced == 0 {
			return 0, store.ErrOldNotFound
		}
		replaced = 1
		content = strings.Replace(content, old, newStr, 1)
	}

	if replaced == 0 {
		return 0, store.ErrOldNotFound
	}

	t.Put(p, content)
	return replaced, nil
}

func (t *Tree) deleteRecursive(p string) {
	node := t.nodes[p]
	if node.Kind == KindDir {
		for _, child := range t.children[p] {
			t.deleteRecursive(child)
		}
		delete(t.children, p)
	}
	delete(t.nodes, p)
	delete(t.content, p)
}

func (t *Tree) removeFromParent(p string) {
	parent := path.Dir(p)
	children := t.children[parent]
	for i, c := range children {
		if c == p {
			t.children[parent] = append(children[:i], children[i+1:]...)
			break
		}
	}
	if len(t.children[parent]) == 0 && parent != "/" {
		if _, ok := t.nodes[parent]; ok && t.nodes[parent].Kind == KindDir {
			delete(t.children, parent)
			t.removeFromParent(parent)
			delete(t.nodes, parent)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func (t *Tree) addParents(dir string) {
	dir = cleanPath(dir)
	if dir == "/" {
		return
	}

	parent := path.Dir(dir)
	t.addParents(parent)
	if _, ok := t.nodes[dir]; ok {
		return
	}

	t.nodes[dir] = Node{Path: dir, Name: path.Base(dir), Kind: KindDir}
	t.children[parent] = appendChild(t.children[parent], dir)
}

func (t *Tree) mustDir(p string) (Node, error) {
	nodePath := cleanPath(p)
	node, ok := t.nodes[nodePath]
	if !ok {
		return Node{}, fmt.Errorf("%w: %s", store.ErrNotFound, nodePath)
	}
	if node.Kind != KindDir {
		return Node{}, fmt.Errorf("%w: %s", store.ErrNotDir, nodePath)
	}
	return node, nil
}

func (t *Tree) writeTree(out *strings.Builder, dir string, level, depth int, opts TreeOptions) {
	if depth >= 0 && level > depth {
		return
	}

	children := make([]Node, 0, len(t.children[dir]))
	for _, child := range t.children[dir] {
		children = append(children, t.nodes[child])
	}

	if !opts.All {
		filtered := make([]Node, 0, len(children))
		for _, n := range children {
			if !isHidden(n.Name) {
				filtered = append(filtered, n)
			}
		}
		children = filtered
	}

	if opts.DirsOnly {
		filtered := make([]Node, 0, len(children))
		for _, n := range children {
			if n.Kind == KindDir {
				filtered = append(filtered, n)
			}
		}
		children = filtered
	}

	sort.SliceStable(children, func(i, j int) bool {
		less, equal := compareField(children[i], children[j], opts.Sort)
		if equal {
			return false
		}
		return less
	})

	if opts.DirsFirst {
		dirs := make([]Node, 0)
		files := make([]Node, 0)
		for _, n := range children {
			if n.Kind == KindDir {
				dirs = append(dirs, n)
			} else {
				files = append(files, n)
			}
		}
		children = append(dirs, files...)
	}

	for _, node := range children {
		out.WriteString(strings.Repeat("  ", level))
		if opts.FullPath {
			out.WriteString(node.Path)
		} else {
			out.WriteString(node.Name)
		}
		if node.Kind == KindDir {
			out.WriteByte('/')
		}
		if opts.ShowSize && node.Kind == KindFile {
			fmt.Fprintf(out, " [%d]", node.Size)
		}
		out.WriteByte('\n')
		if node.Kind == KindDir {
			t.writeTree(out, node.Path, level+1, depth, opts)
		}
	}
}

func (t *Tree) filePathsUnder(root string) []string {
	var paths []string
	for nodePath, node := range t.nodes {
		if node.Kind != KindFile || !under(root, nodePath) {
			continue
		}
		paths = append(paths, nodePath)
	}
	sort.Strings(paths)
	return paths
}

func (t *Tree) allNodesUnder(root string) []Node {
	var nodes []Node
	for nodePath, node := range t.nodes {
		if node.Path == root {
			continue
		}
		if under(root, nodePath) {
			nodes = append(nodes, node)
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Path < nodes[j].Path
	})
	return nodes
}

func relativeDepth(root, p string) int {
	rel := p
	if root == "/" {
		rel = strings.TrimPrefix(p, "/")
	} else {
		rel = strings.TrimPrefix(p, root+"/")
	}
	if rel == "" {
		return 0
	}
	return strings.Count(rel, "/") + 1
}

func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

func cleanFilePath(p string) (string, error) {
	filePath := cleanPath(p)
	if filePath == "/" {
		return "", fmt.Errorf("file path cannot be root")
	}
	return filePath, nil
}

func appendChild(children []string, child string) []string {
	for _, existing := range children {
		if existing == child {
			return children
		}
	}
	return append(children, child)
}

func under(root, p string) bool {
	if root == "/" {
		return true
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

func grepLineMatches(line, pattern string, re *regexp.Regexp, opts GrepOptions) bool {
	var matched bool
	switch {
	case opts.WholeLine:
		matched = wholeLineMatch(line, pattern, re, opts.CaseInsensitive)
	case opts.WholeWord:
		matched = wholeWordMatch(line, pattern, re, opts.CaseInsensitive)
	default:
		matched = grepBasicMatch(line, pattern, re, opts.CaseInsensitive)
	}
	if opts.Invert {
		return !matched
	}
	return matched
}

func grepBasicMatch(line, pattern string, re *regexp.Regexp, caseInsensitive bool) bool {
	if re != nil {
		return re.MatchString(line)
	}
	if caseInsensitive {
		return strings.Contains(strings.ToLower(line), strings.ToLower(pattern))
	}
	return strings.Contains(line, pattern)
}

func wholeLineMatch(line, pattern string, re *regexp.Regexp, caseInsensitive bool) bool {
	if re != nil {
		return re.MatchString(line)
	}
	if caseInsensitive {
		return strings.EqualFold(line, pattern)
	}
	return line == pattern
}

func wholeWordMatch(line, pattern string, re *regexp.Regexp, caseInsensitive bool) bool {
	if re != nil {
		return re.MatchString(line)
	}
	if caseInsensitive {
		return hasWholeWordSubstring(strings.ToLower(line), strings.ToLower(pattern))
	}
	return hasWholeWordSubstring(line, pattern)
}

func hasWholeWordSubstring(line, pattern string) bool {
	idx := strings.Index(line, pattern)
	for idx != -1 {
		if isWordBoundary(line, idx) && isWordBoundary(line, idx+len(pattern)) {
			return true
		}
		next := strings.Index(line[idx+1:], pattern)
		if next == -1 {
			return false
		}
		idx = idx + 1 + next
	}
	return false
}

func isWordBoundary(s string, pos int) bool {
	if pos <= 0 || pos >= len(s) {
		return true
	}
	return !isWordChar(s[pos-1]) || !isWordChar(s[pos])
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func filterHiddenPaths(paths []string, root string) []string {
	var result []string
	for _, p := range paths {
		if !pathHasHiddenComponent(p, root) {
			result = append(result, p)
		}
	}
	return result
}

func pathHasHiddenComponent(p, root string) bool {
	rel := p
	if root != "/" && root != "" {
		rel = strings.TrimPrefix(p, root+"/")
	} else if root == "/" {
		rel = strings.TrimPrefix(p, "/")
	}
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func filterByGlob(paths []string, include, exclude string) []string {
	if include == "" && exclude == "" {
		return paths
	}
	var result []string
	for _, p := range paths {
		name := path.Base(p)
		if include != "" {
			if ok, _ := path.Match(include, name); !ok {
				continue
			}
		}
		if exclude != "" {
			if ok, _ := path.Match(exclude, name); ok {
				continue
			}
		}
		result = append(result, p)
	}
	return result
}

func copyLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	cp := make([]string, len(lines))
	copy(cp, lines)
	return cp
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
