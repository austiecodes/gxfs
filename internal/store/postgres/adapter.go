package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gxfs/internal/store"
	"gxfs/internal/vfs"
)

type Config struct {
	DSN    string
	Schema string
	Files  FileTableConfig
}

type FileTableConfig struct {
	Table         string
	PathColumn    string
	ContentColumn string
	SizeColumn    string
	MTimeColumn   string
}

type Adapter struct {
	pool *pgxpool.Pool
	cfg  Config

	mu   sync.Mutex
	tree *vfs.Tree
}

var _ store.Adapter = (*Adapter)(nil)

func New(pool *pgxpool.Pool, cfg Config) *Adapter {
	return &Adapter{pool: pool, cfg: cfg}
}

func Connect(ctx context.Context, cfg Config) (*Adapter, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return New(pool, cfg), nil
}

func (a *Adapter) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	tree, err := a.treeFor(ctx)
	if err != nil {
		return nil, err
	}
	opts := vfs.LSOptions{
		Sort:      req.Sort,
		Reverse:   req.Reverse,
		Recursive: req.Recursive,
		All:       req.All,
	}
	nodes, err := tree.LS(req.Path, opts)
	if err != nil {
		return nil, err
	}
	return &store.LSResponse{Nodes: nodes}, nil
}

func (a *Adapter) Tree(ctx context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	tree, err := a.treeFor(ctx)
	if err != nil {
		return nil, err
	}
	root, err := tree.Stat(req.Path)
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
	text, err := tree.Tree(req.Path, req.Depth, opts)
	if err != nil {
		return nil, err
	}
	return &store.TreeResponse{Root: root, Text: text}, nil
}

func (a *Adapter) Cat(ctx context.Context, req store.CatRequest) (*store.CatResponse, error) {
	tree, err := a.treeFor(ctx)
	if err != nil {
		return nil, err
	}
	content, err := tree.Cat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.CatResponse{Path: req.Path, Content: content}, nil
}

func (a *Adapter) Grep(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	tree, err := a.treeFor(ctx)
	if err != nil {
		return nil, err
	}
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
	matches, err := tree.Grep(req.Path, req.Pattern, req.Regex, opts)
	if err != nil {
		return nil, err
	}
	return &store.GrepResponse{Matches: matches}, nil
}

func (a *Adapter) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	tree, err := a.treeFor(ctx)
	if err != nil {
		return nil, err
	}
	opts := vfs.FindOptions{
		Type:     req.Type,
		MaxDepth: req.MaxDepth,
		MinDepth: req.MinDepth,
		All:      req.All,
		IName:    req.IName,
	}
	nodes, err := tree.Find(req.Path, req.Name, opts)
	if err != nil {
		return nil, err
	}
	return &store.FindResponse{Nodes: nodes}, nil
}

func (a *Adapter) Stat(ctx context.Context, req store.StatRequest) (*store.StatResponse, error) {
	tree, err := a.treeFor(ctx)
	if err != nil {
		return nil, err
	}
	node, err := tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.StatResponse{Node: node}, nil
}

func (a *Adapter) treeFor(ctx context.Context) (*vfs.Tree, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.tree != nil {
		return a.tree, nil
	}
	if a.pool == nil {
		return nil, fmt.Errorf("postgres pool is nil")
	}

	tree, err := a.loadTree(ctx)
	if err != nil {
		return nil, err
	}
	a.tree = tree
	return tree, nil
}

func (a *Adapter) loadTree(ctx context.Context) (*vfs.Tree, error) {
	query, err := ListFilesSQL(a.cfg)
	if err != nil {
		return nil, err
	}
	rows, err := a.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query postgres files: %w", err)
	}
	defer rows.Close()

	var files []vfs.File
	for rows.Next() {
		var file vfs.File
		var mtime time.Time
		if err := rows.Scan(&file.Path, &file.Content, &file.Size, &mtime); err != nil {
			return nil, fmt.Errorf("scan postgres file: %w", err)
		}
		file.ModTime = mtime.UTC().Format(time.RFC3339)
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read postgres files: %w", err)
	}

	tree, err := vfs.New(files)
	if err != nil {
		return nil, fmt.Errorf("build postgres vfs tree: %w", err)
	}
	return tree, nil
}
