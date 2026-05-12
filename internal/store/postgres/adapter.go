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
	DSN            string
	Schema         string
	Repo           string
	NodesTable     string
	ContentTable   string
	RepoNodesTable string
	Files          FileTableConfig
	CacheTTL       time.Duration
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
	cachedTrees map[string]*cachedTree
	contentMu   sync.Mutex
}

var _ store.Adapter = (*Adapter)(nil)

type cachedTree struct {
	tree     *vfs.Tree
	loadedAt time.Time
}

func New(pool *pgxpool.Pool, cfg Config) *Adapter {
	return &Adapter{pool: pool, cfg: cfg, cachedTrees: make(map[string]*cachedTree)}
}

func Connect(ctx context.Context, cfg Config) (*Adapter, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	adapter := New(pool, cfg)
	if err := adapter.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate postgres schema: %w", err)
	}
	return adapter, nil
}

func (a *Adapter) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
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
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
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
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}
	if err := a.ensureContent(ctx, tree, req.Path); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	content, err := tree.Cat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.CatResponse{Path: req.Path, Content: content}, nil
}

func (a *Adapter) Grep(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}
	if err := a.ensureContentUnder(ctx, tree, req.Path); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
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

func (a *Adapter) Search(ctx context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, store.ErrEmptyQuery
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	repo := a.repo(req.Repo)
	searchQuery, err := SearchSQL(a.cfg)
	if err != nil {
		return nil, err
	}
	pathFilter := ""
	if req.Path != "" && req.Path != "/" {
		pathFilter = path.Clean("/" + strings.TrimSpace(req.Path))
	}
	rows, err := a.pool.Query(ctx, searchQuery, repo, query, pathFilter, limit)
	if err != nil {
		return nil, fmt.Errorf("search postgres: %w", err)
	}
	defer rows.Close()

	var results []store.SearchResult
	total := 0
	for rows.Next() {
		var r store.SearchResult
		var mtime pgtype.Timestamptz
		if err := rows.Scan(&r.Path, &r.Rank, &r.Snippet, &r.Size, &mtime, &total); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		if mtime.Valid {
			r.ModTime = mtime.Time.UTC().Format(time.RFC3339)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read search results: %w", err)
	}
	if results == nil {
		results = []store.SearchResult{}
	}
	return &store.SearchResponse{Results: results, Total: total}, nil
}

func (a *Adapter) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
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
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	node, err := tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.StatResponse{Node: node}, nil
}

func (a *Adapter) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	if err := a.writeBackPut(ctx, repo, req); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	cache := a.cachedTrees[repo]
	if cache == nil {
		cache = &cachedTree{tree: tree, loadedAt: time.Now()}
		a.cachedTrees[repo] = cache
	}
	cache.tree.Put(req.Path, req.Content)
	node, err := cache.tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}
	return &store.PutResponse{Node: node}, nil
}

func (a *Adapter) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	if err := a.writeBackDelete(ctx, repo, tree, req); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	cache := a.cachedTrees[repo]
	if cache == nil {
		cache = &cachedTree{tree: tree, loadedAt: time.Now()}
		a.cachedTrees[repo] = cache
	}
	if err := cache.tree.Delete(req.Path); err != nil {
		return nil, err
	}
	return &store.DeleteResponse{}, nil
}

func (a *Adapter) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	repo := a.repo(req.Repo)
	tree, err := a.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	if err := a.ensureContent(ctx, tree, req.Path); err != nil {
		return nil, err
	}

	a.mu.Lock()
	replaced, err := tree.Edit(req.Path, req.Old, req.New, req.All)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	content, catErr := tree.Cat(req.Path)
	if catErr != nil {
		a.mu.Unlock()
		return nil, catErr
	}
	a.mu.Unlock()
	if err := a.writeBackPut(ctx, repo, store.PutRequest{Path: req.Path, Content: content}); err != nil {
		return nil, err
	}

	return &store.EditResponse{Path: req.Path, Replaced: replaced, Content: content}, nil
}

func (a *Adapter) repo(reqRepo string) string {
	if reqRepo != "" {
		return reqRepo
	}
	return a.cfg.Repo
}

func (a *Adapter) writeBackPut(ctx context.Context, repo string, req store.PutRequest) error {
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
	if _, err := tx.Exec(ctx, upsertRepo, repo, req.Path); err != nil {
		return fmt.Errorf("upsert repo node: %w", err)
	}

	// ensure parent dirs exist in both tables
	filePath := path.Clean("/" + req.Path)
	dir := path.Dir(filePath)
	for dir != "/" {
		if _, err := tx.Exec(ctx, upsertNode, dir, vfs.KindDir, 0); err != nil {
			return fmt.Errorf("upsert parent node %s: %w", dir, err)
		}
		if _, err := tx.Exec(ctx, upsertRepo, repo, dir); err != nil {
			return fmt.Errorf("upsert parent repo node %s: %w", dir, err)
		}
		dir = path.Dir(dir)
	}

	return tx.Commit(ctx)
}

func (a *Adapter) writeBackDelete(ctx context.Context, repo string, tree *vfs.Tree, req store.DeleteRequest) error {
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
	paths := a.collectPathsForDelete(tree, req.Path)
	for _, p := range paths {
		if _, err := tx.Exec(ctx, deleteRepo, repo, p); err != nil {
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

func (a *Adapter) collectPathsForDelete(tree *vfs.Tree, p string) []string {
	p = path.Clean("/" + p)
	node, err := tree.Stat(p)
	if err == nil && node.Kind == vfs.KindDir {
		paths := []string{p}
		a.collectChildPaths(tree, p, &paths)
		return paths
	}
	return []string{p}
}

func (a *Adapter) collectChildPaths(tree *vfs.Tree, dir string, paths *[]string) {
	nodes, err := tree.LS(dir, vfs.LSOptions{All: true})
	if err != nil {
		return
	}
	for _, n := range nodes {
		*paths = append(*paths, n.Path)
		if n.Kind == vfs.KindDir {
			a.collectChildPaths(tree, n.Path, paths)
		}
	}
}

func (a *Adapter) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedTrees = make(map[string]*cachedTree)
}

func (a *Adapter) ensureSchema(ctx context.Context) error {
	statements, err := SchemaSQL(a.cfg)
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if _, err := a.pool.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) treeFor(ctx context.Context, repo string) (*vfs.Tree, error) {
	a.mu.RLock()
	if cache := a.cachedTrees[repo]; cache != nil && !a.expired(cache) {
		tree := cache.tree
		a.mu.RUnlock()
		return tree, nil
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	if cache := a.cachedTrees[repo]; cache != nil && !a.expired(cache) {
		return cache.tree, nil
	}
	if a.pool == nil {
		return nil, fmt.Errorf("postgres pool is nil")
	}

	tree, err := a.loadTree(ctx, repo)
	if err != nil {
		return nil, err
	}
	a.cachedTrees[repo] = &cachedTree{tree: tree, loadedAt: time.Now()}
	return tree, nil
}

func (a *Adapter) expired(cache *cachedTree) bool {
	if a.cfg.CacheTTL <= 0 {
		return false
	}
	return time.Since(cache.loadedAt) >= a.cfg.CacheTTL
}

func (a *Adapter) loadTree(ctx context.Context, repo string) (*vfs.Tree, error) {
	query, err := ListNodesSQL(a.cfg)
	if err != nil {
		return nil, err
	}
	rows, err := a.pool.Query(ctx, query, repo)
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
	a.mu.Lock()
	tree.SetContent(path, content)
	a.mu.Unlock()
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

	loaded := make(map[string]string)
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return fmt.Errorf("scan content: %w", err)
		}
		loaded[path] = content
	}
	if err := rows.Err(); err != nil {
		return err
	}

	a.mu.Lock()
	for path, content := range loaded {
		tree.SetContent(path, content)
	}
	a.mu.Unlock()
	return nil
}
