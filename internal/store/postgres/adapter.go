package postgres

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"gxfs/internal/store"
	"gxfs/internal/vfs"
)

type Config struct {
	DSN             string
	Schema          string
	Repo            string
	NodesTable      string
	ContentTable    string
	RepoNodesTable  string
	Files           FileTableConfig
	CacheTTL        time.Duration
}

type FileTableConfig struct {
	PathColumn  string
	KindColumn  string
	SizeColumn  string
	MTimeColumn string
}

type Adapter struct {
	pool *pgxpool.Pool
	cfg  Config

	mu          sync.RWMutex
	cachedTree  *vfs.Tree
	treeLoadedAt time.Time
	contentMu   sync.Mutex
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
	if err := a.ensureContent(ctx, tree, req.Path); err != nil {
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
	if err := a.ensureContentUnder(ctx, tree, req.Path); err != nil {
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

func (a *Adapter) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	if _, err := a.treeFor(ctx); err != nil {
		return nil, err
	}

	if err := a.writeBackPut(ctx, req); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.cachedTree.Put(req.Path, req.Content)
	node, err := a.cachedTree.Stat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.PutResponse{Node: node}, nil
}

func (a *Adapter) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	if _, err := a.treeFor(ctx); err != nil {
		return nil, err
	}

	if err := a.writeBackDelete(ctx, req); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.cachedTree.Delete(req.Path); err != nil {
		return nil, err
	}
	return &store.DeleteResponse{}, nil
}

func (a *Adapter) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	tree, err := a.treeFor(ctx)
	if err != nil {
		return nil, err
	}

	if err := a.ensureContent(ctx, tree, req.Path); err != nil {
		return nil, err
	}

	replaced, err := tree.Edit(req.Path, req.Old, req.New, req.All)
	if err != nil {
		return nil, err
	}

	content, _ := tree.Cat(req.Path)
	if err := a.writeBackPut(ctx, store.PutRequest{Path: req.Path, Content: content}); err != nil {
		return nil, err
	}

	return &store.EditResponse{Path: req.Path, Replaced: replaced, Content: content}, nil
}

func (a *Adapter) writeBackPut(ctx context.Context, req store.PutRequest) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	size := int64(len(req.Content))

	upsertNode, err := UpsertNodeSQL(a.cfg)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, upsertNode, req.Path, vfs.KindFile, size); err != nil {
		return fmt.Errorf("upsert node: %w", err)
	}

	upsertContent, err := UpsertContentSQL(a.cfg)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, upsertContent, req.Path, req.Content); err != nil {
		return fmt.Errorf("upsert content: %w", err)
	}

	upsertRepo, err := UpsertRepoNodeSQL(a.cfg)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, upsertRepo, a.cfg.Repo, req.Path); err != nil {
		return fmt.Errorf("upsert repo node: %w", err)
	}

	// ensure parent dirs exist in both tables
	filePath := path.Clean("/" + req.Path)
	dir := path.Dir(filePath)
	for dir != "/" {
		if _, err := tx.Exec(ctx, upsertNode, dir, vfs.KindDir, 0); err != nil {
			return fmt.Errorf("upsert parent node %s: %w", dir, err)
		}
		if _, err := tx.Exec(ctx, upsertRepo, a.cfg.Repo, dir); err != nil {
			return fmt.Errorf("upsert parent repo node %s: %w", dir, err)
		}
		dir = path.Dir(dir)
	}

	return tx.Commit(ctx)
}

func (a *Adapter) writeBackDelete(ctx context.Context, req store.DeleteRequest) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	deleteRepo, err := DeleteRepoNodeSQL(a.cfg)
	if err != nil {
		return err
	}

	// collect all paths to delete (node + children if dir)
	paths := a.collectPathsForDelete(req.Path)
	for _, p := range paths {
		if _, err := tx.Exec(ctx, deleteRepo, a.cfg.Repo, p); err != nil {
			return fmt.Errorf("delete repo node %s: %w", p, err)
		}
	}

	// clean orphan nodes (no repo references left)
	cleanOrphan, err := CleanOrphanNodeSQL(a.cfg)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := tx.Exec(ctx, cleanOrphan, p); err != nil {
			return fmt.Errorf("clean orphan %s: %w", p, err)
		}
	}

	return tx.Commit(ctx)
}

func (a *Adapter) collectPathsForDelete(p string) []string {
	p = path.Clean("/" + p)
	node, err := a.cachedTree.Stat(p)
	if err == nil && node.Kind == vfs.KindDir {
		paths := []string{p}
		a.collectChildPaths(p, &paths)
		return paths
	}
	return []string{p}
}

func (a *Adapter) collectChildPaths(dir string, paths *[]string) {
	nodes, err := a.cachedTree.LS(dir, vfs.LSOptions{All: true})
	if err != nil {
		return
	}
	for _, n := range nodes {
		*paths = append(*paths, n.Path)
		if n.Kind == vfs.KindDir {
			a.collectChildPaths(n.Path, paths)
		}
	}
}

func (a *Adapter) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedTree = nil
	a.treeLoadedAt = time.Time{}
}

func (a *Adapter) treeFor(ctx context.Context) (*vfs.Tree, error) {
	a.mu.RLock()
	if a.cachedTree != nil && !a.expired() {
		tree := a.cachedTree
		a.mu.RUnlock()
		return tree, nil
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cachedTree != nil && !a.expired() {
		return a.cachedTree, nil
	}
	if a.pool == nil {
		return nil, fmt.Errorf("postgres pool is nil")
	}

	tree, err := a.loadTree(ctx)
	if err != nil {
		return nil, err
	}
	a.cachedTree = tree
	a.treeLoadedAt = time.Now()
	return tree, nil
}

func (a *Adapter) expired() bool {
	return time.Since(a.treeLoadedAt) >= a.cfg.CacheTTL
}

func (a *Adapter) loadTree(ctx context.Context) (*vfs.Tree, error) {
	query, err := ListNodesSQL(a.cfg)
	if err != nil {
		return nil, err
	}
	rows, err := a.pool.Query(ctx, query, a.cfg.Repo)
	if err != nil {
		return nil, fmt.Errorf("query postgres nodes: %w", err)
	}
	defer rows.Close()

	var files []vfs.File
	for rows.Next() {
		var path, kind string
		var size int64
		var mtime pgtype.Timestamptz
		if err := rows.Scan(&path, &kind, &size, &mtime); err != nil {
			return nil, fmt.Errorf("scan postgres node: %w", err)
		}
		if kind == vfs.KindDir {
			continue
		}
		file := vfs.File{Path: path, Size: size}
		if mtime.Valid {
			file.ModTime = mtime.Time.UTC().Format(time.RFC3339)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read postgres nodes: %w", err)
	}

	tree, err := vfs.New(files)
	if err != nil {
		return nil, fmt.Errorf("build postgres vfs tree: %w", err)
	}
	return tree, nil
}

func (a *Adapter) ensureContent(ctx context.Context, tree *vfs.Tree, path string) error {
	if tree.HasContent(path) {
		return nil
	}

	a.contentMu.Lock()
	defer a.contentMu.Unlock()

	if tree.HasContent(path) {
		return nil
	}

	query, err := LoadContentSQL(a.cfg)
	if err != nil {
		return err
	}
	var content string
	if err := a.pool.QueryRow(ctx, query, path).Scan(&content); err != nil {
		return fmt.Errorf("load content for %s: %w", path, err)
	}
	tree.SetContent(path, content)
	return nil
}

func (a *Adapter) ensureContentUnder(ctx context.Context, tree *vfs.Tree, rootPath string) error {
	a.contentMu.Lock()
	defer a.contentMu.Unlock()

	query, err := LoadContentUnderSQL(a.cfg)
	if err != nil {
		return err
	}
	prefix := rootPath
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if rootPath == "/" {
		prefix = "/"
	}
	rows, err := a.pool.Query(ctx, query, prefix+"%")
	if err != nil {
		return fmt.Errorf("load content under %s: %w", rootPath, err)
	}
	defer rows.Close()

	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return fmt.Errorf("scan content: %w", err)
		}
		tree.SetContent(path, content)
	}
	return rows.Err()
}
