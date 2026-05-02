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
	nodes, err := a.tree.LS(req.Path)
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
	text, err := a.tree.Tree(req.Path, req.Depth)
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
	matches, err := a.tree.Grep(req.Path, req.Pattern, req.Regex)
	if err != nil {
		return nil, err
	}
	return &store.GrepResponse{Matches: matches}, nil
}

func (a *Adapter) Find(_ context.Context, req store.FindRequest) (*store.FindResponse, error) {
	nodes, err := a.tree.Find(req.Path, req.Name)
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
