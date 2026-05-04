package memory

import (
	"context"

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
	return &store.LSResponse{Nodes: nodes}, nil
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
	return &store.CatResponse{Path: req.Path, Content: content}, nil
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
	return &store.FindResponse{Nodes: nodes}, nil
}

func (a *Adapter) Stat(_ context.Context, req store.StatRequest) (*store.StatResponse, error) {
	node, err := a.tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.StatResponse{Node: node}, nil
}
