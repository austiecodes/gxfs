package postgres

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
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
	return &store.LSResponse{Nodes: paginateNodes(nodes, req.Limit, req.Offset), Total: len(nodes)}, nil
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

	// Verify root path exists and is a directory (same as VFS grep's mustDir check).
	statResp, err := a.Stat(ctx, store.StatRequest{Repo: req.Repo, Path: req.Path})
	if err != nil {
		return nil, err
	}
	if statResp.Node.Kind != "dir" {
		return nil, fmt.Errorf("%w: %s", store.ErrNotDir, req.Path)
	}

	// Streaming grep: SQL streams (path, content) rows for matching files,
	// Go does line-level matching with full grep semantics.
	query, err := StreamGrepSQL(a.cfg)
	if err != nil {
		return nil, err
	}

	prefix := path.Clean("/" + strings.TrimSuffix(req.Path, "/"))
	if prefix != "/" {
		prefix += "/"
	}

	rows, err := a.pool.Query(ctx, query, repo, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("grep query: %w", err)
	}
	defer rows.Close()

	// Compile regex once if needed.
	effectivePattern := req.Pattern
	if req.Regex {
		if req.CaseInsensitive {
			effectivePattern = "(?i)" + effectivePattern
		}
		if req.WholeWord {
			effectivePattern = `\b` + effectivePattern + `\b`
		}
		if req.WholeLine {
			effectivePattern = `^` + effectivePattern + `$`
		}
	}

	var re *regexp.Regexp
	if req.Regex {
		compiled, err := regexp.Compile(effectivePattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		re = compiled
	}

	// No pre-compiled regex needed; globMatch uses path.Match on basename.

	var matches []store.Match
	for rows.Next() {
		var filePath, content string
		if err := rows.Scan(&filePath, &content); err != nil {
			return nil, fmt.Errorf("grep scan: %w", err)
		}

		// Path-level filtering (same as VFS grep).
		if !req.All && pathHasHidden(filePath, prefix) {
			continue
		}
		if !globMatch(filePath, req.Include, req.Exclude) {
			continue
		}

		// Line-level matching (same logic as VFS grep).
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			matched := grepLineMatch(line, req.Pattern, re, req.CaseInsensitive, req.WholeWord, req.WholeLine)
			if req.Invert {
				matched = !matched
			}
			if matched {
				m := store.Match{
					Path: filePath,
					Line: i + 1,
					Text: line,
				}
				if req.ContextBefore > 0 {
					start := max(0, i-req.ContextBefore)
					m.Before = copyLines(lines[start:i])
				}
				if req.ContextAfter > 0 {
					end := min(len(lines), i+1+req.ContextAfter)
					m.After = copyLines(lines[i+1 : end])
				}
				matches = append(matches, m)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("grep rows: %w", err)
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
	pathFilter := ""
	if req.Path != "" && req.Path != "/" {
		pathFilter = path.Clean("/" + strings.TrimSpace(req.Path))
	}

	// Run count query first so total is correct even when offset >= total.
	countQuery, err := SearchCountSQL(a.cfg)
	if err != nil {
		return nil, err
	}
	var total int
	if err := a.pool.QueryRow(ctx, countQuery, repo, query, pathFilter).Scan(&total); err != nil {
		return nil, fmt.Errorf("search count: %w", err)
	}
	if total == 0 {
		return &store.SearchResponse{Results: []store.SearchResult{}, Total: 0}, nil
	}

	dataQuery, err := SearchDataSQL(a.cfg)
	if err != nil {
		return nil, err
	}
	rows, err := a.pool.Query(ctx, dataQuery, repo, query, pathFilter, limit, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("search postgres: %w", err)
	}
	defer rows.Close()

	var results []store.SearchResult
	for rows.Next() {
		var r store.SearchResult
		var mtime pgtype.Timestamptz
		if err := rows.Scan(&r.Path, &r.Rank, &r.Snippet, &r.Size, &mtime); err != nil {
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
	return &store.FindResponse{Nodes: paginateNodes(nodes, req.Limit, req.Offset), Total: len(nodes)}, nil
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
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("load content for %s: %w", path, store.ErrNotFound)
		}
		return fmt.Errorf("load content for %s: %w", path, err)
	}
	a.mu.Lock()
	tree.SetContent(path, content)
	a.mu.Unlock()
	return nil
}

// grepLineMatch implements the same matching logic as vfs.grepLineMatches.
func grepLineMatch(line, pattern string, re *regexp.Regexp, caseInsensitive, wholeWord, wholeLine bool) bool {
	switch {
	case wholeLine:
		return wholeLineMatch(line, pattern, re, caseInsensitive)
	case wholeWord:
		return wholeWordMatch(line, pattern, re, caseInsensitive)
	default:
		return grepBasicMatch(line, pattern, re, caseInsensitive)
	}
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

// hasWholeWordSubstring checks if pattern exists in line with word boundaries.
// Ported from vfs.hasWholeWordSubstring to keep semantics identical.
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

// globMatch matches a file path against include/exclude glob patterns.
// Uses path.Base to extract the filename (same as VFS filterByGlob).
// Returns false if include doesn't match or exclude matches.
func globMatch(filePath, include, exclude string) bool {
	name := path.Base(filePath)
	if include != "" {
		if ok, _ := path.Match(include, name); !ok {
			return false
		}
	}
	if exclude != "" {
		if ok, _ := path.Match(exclude, name); ok {
			return false
		}
	}
	return true
}

// pathHasHidden checks if a file path has a hidden component (starting with .).
func pathHasHidden(filePath, root string) bool {
	rel := strings.TrimPrefix(filePath, root)
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if strings.HasPrefix(p, ".") && p != "." && p != ".." {
			return true
		}
	}
	return false
}

func copyLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	cp := make([]string, len(lines))
	copy(cp, lines)
	return cp
}

// paginateNodes applies limit/offset to a node slice.
// Limit <= 0 means unlimited; Offset is clamped to [0, len(nodes)].
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
