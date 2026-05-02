package vfs

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"gxfs/internal/store"
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
		tree.content[filePath] = file.Content
	}

	for parent := range tree.children {
		sort.Slice(tree.children[parent], func(i, j int) bool {
			return tree.nodes[tree.children[parent][i]].Name < tree.nodes[tree.children[parent][j]].Name
		})
	}

	return tree, nil
}

func (t *Tree) LS(p string) ([]Node, error) {
	node, err := t.mustDir(p)
	if err != nil {
		return nil, err
	}

	paths := t.children[node.Path]
	nodes := make([]Node, 0, len(paths))
	for _, child := range paths {
		nodes = append(nodes, t.nodes[child])
	}
	return nodes, nil
}

func (t *Tree) Tree(p string, depth int) (string, error) {
	node, err := t.mustDir(p)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString(node.Name)
	if node.Path == "/" {
		out.Reset()
		out.WriteString("/")
	}
	out.WriteByte('\n')
	t.writeTree(&out, node.Path, 1, depth)
	return out.String(), nil
}

func (t *Tree) Cat(p string) (string, error) {
	filePath := cleanPath(p)
	node, ok := t.nodes[filePath]
	if !ok {
		return "", fmt.Errorf("path not found: %s", filePath)
	}
	if node.Kind == KindDir {
		return "", fmt.Errorf("is a directory: %s", filePath)
	}
	return t.content[filePath], nil
}

func (t *Tree) Grep(root, pattern string, regex bool) ([]Match, error) {
	rootPath := cleanPath(root)
	if _, err := t.mustDir(rootPath); err != nil {
		return nil, err
	}

	var re *regexp.Regexp
	if regex {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		re = compiled
	}

	var matches []Match
	for _, filePath := range t.filePathsUnder(rootPath) {
		lines := strings.Split(t.content[filePath], "\n")
		for i, line := range lines {
			if lineMatches(line, pattern, re) {
				matches = append(matches, Match{
					Path: filePath,
					Line: i + 1,
					Text: line,
				})
			}
		}
	}
	return matches, nil
}

func (t *Tree) Find(root, name string) ([]Node, error) {
	rootPath := cleanPath(root)
	if _, err := t.mustDir(rootPath); err != nil {
		return nil, err
	}

	var nodes []Node
	for _, filePath := range t.filePathsUnder(rootPath) {
		node := t.nodes[filePath]
		ok, err := path.Match(name, node.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid name pattern: %w", err)
		}
		if ok {
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

func (t *Tree) Stat(p string) (Node, error) {
	nodePath := cleanPath(p)
	node, ok := t.nodes[nodePath]
	if !ok {
		return Node{}, fmt.Errorf("path not found: %s", nodePath)
	}
	return node, nil
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
		return Node{}, fmt.Errorf("path not found: %s", nodePath)
	}
	if node.Kind != KindDir {
		return Node{}, fmt.Errorf("not a directory: %s", nodePath)
	}
	return node, nil
}

func (t *Tree) writeTree(out *strings.Builder, dir string, level, depth int) {
	if depth >= 0 && level > depth {
		return
	}

	for _, child := range t.children[dir] {
		node := t.nodes[child]
		out.WriteString(strings.Repeat("  ", level))
		out.WriteString(node.Name)
		if node.Kind == KindDir {
			out.WriteByte('/')
		}
		out.WriteByte('\n')
		if node.Kind == KindDir {
			t.writeTree(out, node.Path, level+1, depth)
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

func lineMatches(line, pattern string, re *regexp.Regexp) bool {
	if re != nil {
		return re.MatchString(line)
	}
	return strings.Contains(line, pattern)
}
