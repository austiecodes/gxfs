package memory

import (
	"context"
	"strings"

	"gxfs/internal/store"
	"gxfs/internal/vfs"
)

type Adapter struct {
	tree *vfs.Tree
}

var _ store.Adapter = (*Adapter)(nil)

func New(tree *vfs.Tree) *Adapter {
	return &Adapter{tree: tree}
}

func (a *Adapter) LS(_ context.Context, req store.LSRequest) (*store.LSResponse, error) {
	opts := vfs.LSOptions{
		Sort:      req.Sort,
		Reverse:   req.Reverse,
		Recursive: req.Recursive,
		All:       req.All,
	}
	nodes, err := a.tree.LS(req.Path, opts)
	if err != nil {
		return nil, err
	}
	return &store.LSResponse{Nodes: paginateNodes(nodes, req.Limit, req.Offset), Total: len(nodes)}, nil
}

func (a *Adapter) Tree(_ context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	root, err := a.tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}
	opts := vfs.TreeOptions{
		All:       req.All,
		DirsOnly:  req.DirsOnly,
		FullPath:  req.FullPath,
		ShowSize:  req.ShowSize,
		Sort:      req.Sort,
		DirsFirst: req.DirsFirst,
	}
	text, err := a.tree.Tree(req.Path, req.Depth, opts)
	if err != nil {
		return nil, err
	}
	return &store.TreeResponse{Root: root, Text: text}, nil
}

func (a *Adapter) Cat(_ context.Context, req store.CatRequest) (*store.CatResponse, error) {
	content, err := a.tree.Cat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.CatResponse{Path: req.Path, Content: content, Hash: store.HashContent(content)}, nil
}

func (a *Adapter) Grep(_ context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	opts := vfs.GrepOptions{
		CaseInsensitive: req.CaseInsensitive,
		Invert:          req.Invert,
		WholeWord:       req.WholeWord,
		WholeLine:       req.WholeLine,
		ContextBefore:   req.ContextBefore,
		ContextAfter:    req.ContextAfter,
		All:             req.All,
		Include:         req.Include,
		Exclude:         req.Exclude,
	}
	matches, err := a.tree.Grep(req.Path, req.Pattern, req.Regex, opts)
	if err != nil {
		return nil, err
	}
	return &store.GrepResponse{Matches: matches}, nil
}

func (a *Adapter) Find(_ context.Context, req store.FindRequest) (*store.FindResponse, error) {
	opts := vfs.FindOptions{
		Type:     req.Type,
		MaxDepth: req.MaxDepth,
		MinDepth: req.MinDepth,
		All:      req.All,
		IName:    req.IName,
	}
	nodes, err := a.tree.Find(req.Path, req.Name, opts)
	if err != nil {
		return nil, err
	}
	return &store.FindResponse{Nodes: paginateNodes(nodes, req.Limit, req.Offset), Total: len(nodes)}, nil
}

func (a *Adapter) Stat(_ context.Context, req store.StatRequest) (*store.StatResponse, error) {
	node, err := a.tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.StatResponse{Node: node}, nil
}

func (a *Adapter) Put(_ context.Context, req store.PutRequest) (*store.PutResponse, error) {
	a.tree.Put(req.Path, req.Content)
	node, err := a.tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.PutResponse{Node: node}, nil
}

func (a *Adapter) Delete(_ context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	if err := a.tree.Delete(req.Path); err != nil {
		return nil, err
	}
	return &store.DeleteResponse{}, nil
}

func (a *Adapter) Edit(_ context.Context, req store.EditRequest) (*store.EditResponse, error) {
	replaced, err := a.tree.Edit(req.Path, req.Old, req.New, req.All)
	if err != nil {
		return nil, err
	}
	content, _ := a.tree.Cat(req.Path)
	return &store.EditResponse{Path: req.Path, Replaced: replaced, Content: content}, nil
}

func (a *Adapter) Search(_ context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, store.ErrEmptyQuery
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	terms := strings.Fields(strings.ToLower(query))

	nodes, err := a.tree.Find(req.Path, "", vfs.FindOptions{Type: "file", All: true})
	if err != nil {
		return nil, err
	}

	var results []store.SearchResult
	total := 0
	for _, n := range nodes {
		content, err := a.tree.Cat(n.Path)
		if err != nil {
			continue
		}
		lowerContent := strings.ToLower(content)
		match := true
		for _, t := range terms {
			if !strings.Contains(lowerContent, t) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		total++
		if total <= req.Offset {
			continue
		}
		if len(results) >= limit {
			continue
		}
		snippet := snippetFromContent(content, terms)
		results = append(results, store.SearchResult{
			Path:    n.Path,
			Rank:    1.0,
			Snippet: snippet,
			Size:    n.Size,
			ModTime: n.ModTime,
		})
	}
	if results == nil {
		results = []store.SearchResult{}
	}
	return &store.SearchResponse{Results: results, Total: total}, nil
}

func snippetFromContent(content string, terms []string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		lower := strings.ToLower(line)
		for _, t := range terms {
			if strings.Contains(lower, t) && len(strings.TrimSpace(line)) > 0 {
				if len(line) > 200 {
					return line[:200] + "..."
				}
				return line
			}
		}
	}
	if len(content) > 200 {
		return content[:200] + "..."
	}
	return content
}

func (a *Adapter) BatchHashes(_ context.Context, req store.HashRequest) (*store.HashResponse, error) {
	nodes, err := a.tree.Find(req.Path, "", vfs.FindOptions{Type: "file", All: true})
	if err != nil {
		return nil, err
	}
	var hashes []store.ContentHash
	for _, n := range nodes {
		content, err := a.tree.Cat(n.Path)
		if err != nil {
			continue
		}
		hashes = append(hashes, store.ContentHash{
			Path: n.Path,
			Hash: store.HashContent(content),
		})
	}
	if hashes == nil {
		hashes = []store.ContentHash{}
	}
	return &store.HashResponse{Hashes: hashes}, nil
}

// paginateNodes applies limit/offset to a node slice.
func paginateNodes(nodes []store.Node, limit, offset int) []store.Node {
	if offset < 0 {
		offset = 0
	}
	if offset > len(nodes) {
		offset = len(nodes)
	}
	nodes = nodes[offset:]
	if limit > 0 && len(nodes) > limit {
		nodes = nodes[:limit]
	}
	return nodes
}
