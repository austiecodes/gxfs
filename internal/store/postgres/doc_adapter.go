package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/austiecodes/rolio/internal/store"
	"github.com/austiecodes/rolio/internal/vfs"
)

// DocAdapter implements store.Adapter over the document-centric tables.
//
// Read methods fall into two categories:
//  1. Structure queries (LS, Find, Stat, Tree): build a vfs.Tree from
//     the configured doc binding table, then delegate to vfs.Tree methods.
//     This guarantees exact behavioral compatibility with the old adapter.
//  2. Content queries (Cat, Search, BatchHashes, Grep): query rolio_docs
//     directly for content, hashes, and full-text search.
//
// Write methods (Put, Delete, Edit) operate on rolio_docs plus the configured
// binding table within transactions. Delete removes the binding only (doc
// preserved for potential cross-scope references). Put/Edit increment revision.
type DocAdapter struct {
	pool        *pgxpool.Pool
	cfg         Config
	mu          sync.RWMutex
	cachedTrees map[string]*docCachedTree
}

type docCachedTree struct {
	tree     *vfs.Tree
	loadedAt time.Time
}

var _ store.Adapter = (*DocAdapter)(nil)

// NewDocAdapter creates a DocAdapter backed by document-centric tables.
func NewDocAdapter(pool *pgxpool.Pool, cfg Config) *DocAdapter {
	return newDocAdapter(pool, withDocRepoBinding(cfg))
}

// NewDocsNamespaceAdapter creates a DocAdapter scoped by docs namespace paths.
func NewDocsNamespaceAdapter(pool *pgxpool.Pool, cfg Config) *DocAdapter {
	return newDocAdapter(pool, withDocNamespaceBinding(cfg))
}

// NewDocsetReadAdapter creates a read-only adapter scoped by curated docset
// membership paths. Docset contents are immutable through docset://; membership
// changes go through the DocsetManager API.
func NewDocsetReadAdapter(pool *pgxpool.Pool, cfg Config) store.Adapter {
	return &readOnlyAdapter{
		Adapter:    newDocAdapter(pool, withDocsetBinding(cfg)),
		sourceKind: store.SourceKindDocset,
		sourceName: cfg.Repo,
	}
}

// NewDocNamespaceAdapter is an alias for NewDocsNamespaceAdapter.
func NewDocNamespaceAdapter(pool *pgxpool.Pool, cfg Config) *DocAdapter {
	return NewDocsNamespaceAdapter(pool, cfg)
}

func newDocAdapter(pool *pgxpool.Pool, cfg Config) *DocAdapter {
	return &DocAdapter{pool: pool, cfg: cfg, cachedTrees: make(map[string]*docCachedTree)}
}

// ConnectDoc creates a DocAdapter by connecting to Postgres, running schema
// migrations, and performing an idempotent backfill from legacy tables.
// This is the doc_postgres equivalent of Connect.
func ConnectDoc(ctx context.Context, cfg Config) (*DocAdapter, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect doc postgres: %w", err)
	}

	// Run all schema migrations (including legacy tables needed for backfill).
	statements, err := SchemaSQL(cfg)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("doc schema sql: %w", err)
	}
	for _, stmt := range statements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			pool.Close()
			return nil, fmt.Errorf("doc schema exec: %w", err)
		}
	}

	// Idempotent backfill from legacy tables.
	result, err := BackfillDocs(ctx, pool, cfg)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("doc backfill: %w", err)
	}
	slog.Info("doc adapter backfill",
		"repo", cfg.Repo,
		"docs_inserted", result.DocsInserted,
		"paths_inserted", result.PathsInserted,
		"hashes_computed", result.HashesComputed,
	)

	return NewDocAdapter(pool, cfg), nil
}

// ConnectDocNamespace creates a docs namespace adapter by connecting to
// Postgres and running schema migrations. It does not backfill legacy repo
// paths into namespace paths.
func ConnectDocNamespace(ctx context.Context, cfg Config) (*DocAdapter, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect doc namespace postgres: %w", err)
	}

	statements, err := SchemaSQL(cfg)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("doc namespace schema sql: %w", err)
	}
	for _, stmt := range statements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			pool.Close()
			return nil, fmt.Errorf("doc namespace schema exec: %w", err)
		}
	}

	return NewDocsNamespaceAdapter(pool, cfg), nil
}

func withDocRepoBinding(cfg Config) Config {
	cfg.DocBinding = DocBindingConfig{PathsTable: docRepoPathsTable, ScopeColumn: docRepoScopeColumn}
	return cfg
}

func withDocNamespaceBinding(cfg Config) Config {
	cfg.DocBinding = DocBindingConfig{PathsTable: docNamespacePathsTable, ScopeColumn: docNamespaceScopeColumn}
	return cfg
}

func withDocsetBinding(cfg Config) Config {
	cfg.DocBinding = DocBindingConfig{PathsTable: docsetPathsView, ScopeColumn: docsetScopeColumn}
	return cfg
}

type readOnlyAdapter struct {
	store.Adapter
	sourceKind store.SourceKind
	sourceName string
}

func (a *readOnlyAdapter) Put(context.Context, store.PutRequest) (*store.PutResponse, error) {
	return nil, store.ErrReadOnlyMount
}

func (a *readOnlyAdapter) Delete(context.Context, store.DeleteRequest) (*store.DeleteResponse, error) {
	return nil, store.ErrReadOnlyMount
}

func (a *readOnlyAdapter) Edit(context.Context, store.EditRequest) (*store.EditResponse, error) {
	return nil, store.ErrReadOnlyMount
}

func (a *readOnlyAdapter) Locate(ctx context.Context, req store.LocateRequest) (*store.LocateResponse, error) {
	resp, err := a.Adapter.Locate(ctx, req)
	if err != nil || resp == nil || a.sourceKind == "" {
		return resp, err
	}
	name := req.Repo
	if name == "" {
		name = a.sourceName
	}
	for i := range resp.Results {
		resp.Results[i].Ref = store.SourceRef{
			Kind: a.sourceKind,
			Name: name,
			Path: resp.Results[i].Path,
		}.String()
	}
	return resp, nil
}

func (a *readOnlyAdapter) Invalidate() {
	if invalidator, ok := a.Adapter.(store.CacheInvalidator); ok {
		invalidator.Invalidate()
	}
}

func (d *DocAdapter) repo(reqRepo string) string {
	if reqRepo != "" {
		return reqRepo
	}
	return d.cfg.Repo
}

// cleanDocPath normalizes a path to match vfs.Tree.cleanPath behavior.
func cleanDocPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// buildTree loads all file paths for a scope from the configured doc binding
// table and builds a vfs.Tree. This mirrors the old adapter's loadTree pattern,
// but reads from the new document-centric tables instead of vfs_nodes.
func (d *DocAdapter) buildTree(ctx context.Context, repo string) (*vfs.Tree, error) {
	query, err := DocListPathsSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	rows, err := d.pool.Query(ctx, query, repo, "/%")
	if err != nil {
		return nil, fmt.Errorf("doc build tree query: %w", err)
	}
	defer rows.Close()

	var files []vfs.File
	for rows.Next() {
		var filePath string
		var size int64
		var mtime time.Time
		if err := rows.Scan(&filePath, &size, &mtime); err != nil {
			return nil, fmt.Errorf("doc build tree scan: %w", err)
		}
		files = append(files, vfs.File{
			Path:    filePath,
			Size:    size,
			ModTime: mtime.UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc build tree rows: %w", err)
	}

	tree, err := vfs.New(files)
	if err != nil {
		return nil, fmt.Errorf("doc build vfs tree: %w", err)
	}
	return tree, nil
}

// treeFor returns a cached vfs.Tree for the repo, rebuilding if expired or missing.
// This matches the old adapter's caching pattern exactly.
func (d *DocAdapter) treeFor(ctx context.Context, repo string) (*vfs.Tree, error) {
	d.mu.RLock()
	if cache := d.cachedTrees[repo]; cache != nil && !d.cacheExpired(cache) {
		tree := cache.tree
		d.mu.RUnlock()
		return tree, nil
	}
	d.mu.RUnlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	// Double-check after acquiring write lock.
	if cache := d.cachedTrees[repo]; cache != nil && !d.cacheExpired(cache) {
		return cache.tree, nil
	}

	tree, err := d.buildTree(ctx, repo)
	if err != nil {
		return nil, err
	}
	d.cachedTrees[repo] = &docCachedTree{tree: tree, loadedAt: time.Now()}
	return tree, nil
}

func (d *DocAdapter) cacheExpired(cache *docCachedTree) bool {
	if d.cfg.CacheTTL <= 0 {
		return false
	}
	return time.Since(cache.loadedAt) >= d.cfg.CacheTTL
}

// Invalidate clears all cached trees, forcing fresh builds on next access.
func (d *DocAdapter) Invalidate() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cachedTrees = make(map[string]*docCachedTree)
}

// invalidateRepo clears the cached tree for a specific repo after a write.
func (d *DocAdapter) invalidateRepo(repo string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.cachedTrees, repo)
}

// --- Read methods ---

func (d *DocAdapter) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	tree, err := d.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	nodes, err := tree.LS(req.Path, vfs.LSOptions{
		Sort:      req.Sort,
		Reverse:   req.Reverse,
		Recursive: req.Recursive,
		All:       req.All,
	})
	if err != nil {
		return nil, err
	}

	total := len(nodes)
	nodes = paginateNodes(nodes, req.Limit, req.Offset)

	return &store.LSResponse{Nodes: nodes, Total: total}, nil
}

func (d *DocAdapter) Cat(ctx context.Context, req store.CatRequest) (*store.CatResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	query, err := DocCatSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	var content, hash string
	if err := d.pool.QueryRow(ctx, query, repo, req.Path).Scan(&content, &hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("doc cat %s: %w", req.Path, store.ErrNotFound)
		}
		return nil, fmt.Errorf("doc cat %s: %w", req.Path, err)
	}

	return &store.CatResponse{Path: req.Path, Content: content, Hash: hash}, nil
}

func (d *DocAdapter) Stat(ctx context.Context, req store.StatRequest) (*store.StatResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	tree, err := d.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	node, err := tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}

	// For file nodes, enrich with hash from doc tables.
	if node.Kind == "file" {
		fileQuery, err := DocStatSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		var filePath string
		var size int64
		var mtime time.Time
		var hash string
		if err := d.pool.QueryRow(ctx, fileQuery, repo, req.Path).Scan(&filePath, &size, &mtime, &hash); err == nil {
			node.Hash = hash
		}
	}

	return &store.StatResponse{Node: node}, nil
}

func (d *DocAdapter) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	tree, err := d.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	nodes, err := tree.Find(req.Path, req.Name, vfs.FindOptions{
		Type:     req.Type,
		MaxDepth: req.MaxDepth,
		MinDepth: req.MinDepth,
		All:      req.All,
		IName:    req.IName,
	})
	if err != nil {
		return nil, err
	}

	total := len(nodes)
	nodes = paginateNodes(nodes, req.Limit, req.Offset)

	return &store.FindResponse{Nodes: nodes, Total: total}, nil
}

func (d *DocAdapter) Search(ctx context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	repo := d.repo(req.Repo)

	pathFilter := cleanDocPath(req.Path)
	if pathFilter == "/" {
		pathFilter = ""
	}

	countQuery, err := DocSearchCountSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	var total int
	if err := d.pool.QueryRow(ctx, countQuery, repo, req.Query, pathFilter).Scan(&total); err != nil {
		return nil, fmt.Errorf("doc search count: %w", err)
	}
	if total == 0 {
		return &store.SearchResponse{Total: 0}, nil
	}

	dataQuery, err := DocSearchDataSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}

	rows, err := d.pool.Query(ctx, dataQuery, repo, req.Query, pathFilter, limit, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("doc search data: %w", err)
	}
	defer rows.Close()

	var results []store.SearchResult
	for rows.Next() {
		var filePath string
		var rank float64
		var snippet string
		var size int64
		var mtime time.Time
		if err := rows.Scan(&filePath, &rank, &snippet, &size, &mtime); err != nil {
			return nil, fmt.Errorf("doc search scan: %w", err)
		}
		results = append(results, store.SearchResult{
			Path:    filePath,
			Rank:    rank,
			Snippet: snippet,
			Size:    size,
			ModTime: mtime.UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc search rows: %w", err)
	}

	return &store.SearchResponse{Results: results, Total: total}, nil
}

func (d *DocAdapter) Locate(ctx context.Context, req store.LocateRequest) (*store.LocateResponse, error) {
	repo := d.repo(req.Repo)

	countQuery, err := DocLocateCountSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	var total int
	if err := d.pool.QueryRow(ctx, countQuery, repo, req.Query).Scan(&total); err != nil {
		return nil, fmt.Errorf("doc locate count: %w", err)
	}
	if total == 0 {
		return &store.LocateResponse{Total: 0}, nil
	}

	dataQuery, err := DocLocateDataSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	rows, err := d.pool.Query(ctx, dataQuery, repo, req.Query, limit, 0)
	if err != nil {
		return nil, fmt.Errorf("doc locate data: %w", err)
	}
	defer rows.Close()

	var results []store.LocateResult
	for rows.Next() {
		var filePath string
		var rank float64
		var snippet string
		if err := rows.Scan(&filePath, &rank, &snippet); err != nil {
			return nil, fmt.Errorf("doc locate scan: %w", err)
		}
		results = append(results, store.LocateResult{
			Ref:     "repo://" + url.PathEscape(repo) + filePath,
			Path:    filePath,
			Score:   rank,
			Snippet: snippet,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc locate rows: %w", err)
	}

	return &store.LocateResponse{Results: results, Total: total}, nil
}

func (d *DocAdapter) BatchHashes(ctx context.Context, req store.HashRequest) (*store.HashResponse, error) {
	repo := d.repo(req.Repo)

	pathFilter := cleanDocPath(req.Path)
	if pathFilter == "/" {
		pathFilter = ""
	}

	query, err := DocBatchHashesSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	rows, err := d.pool.Query(ctx, query, repo, pathFilter)
	if err != nil {
		return nil, fmt.Errorf("doc batch hashes: %w", err)
	}
	defer rows.Close()

	var hashes []store.ContentHash
	for rows.Next() {
		var filePath, hash string
		if err := rows.Scan(&filePath, &hash); err != nil {
			return nil, fmt.Errorf("doc batch hashes scan: %w", err)
		}
		hashes = append(hashes, store.ContentHash{Path: filePath, Hash: hash})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc batch hashes rows: %w", err)
	}

	return &store.HashResponse{Hashes: hashes}, nil
}

func (d *DocAdapter) Glob(ctx context.Context, req store.GlobRequest) (*store.GlobResponse, error) {
	if req.Pattern == "" {
		return nil, fmt.Errorf("%w: pattern is required", store.ErrInvalidParam)
	}

	repo := d.repo(req.Repo)
	regex, err := globToRegex(req.Pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", store.ErrInvalidParam, err)
	}

	// DB paths have leading /; globToRegex assumes no leading /.
	// Add /? anchor so regex matches both /docs/x.md and docs/x.md.
	anchored := "^/?" + regex + "$"

	// Count total matches.
	countSQL, err := DocGlobCountSQL(d.cfg)
	if err != nil {
		return nil, err
	}
	var total int
	if err := d.pool.QueryRow(ctx, countSQL, repo, anchored).Scan(&total); err != nil {
		return nil, fmt.Errorf("doc glob count: %w", err)
	}

	if total == 0 {
		return &store.GlobResponse{Total: 0}, nil
	}

	// Fetch results.
	var rows pgx.Rows
	if req.Limit > 0 {
		dataSQL, err := DocGlobDataSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		rows, err = d.pool.Query(ctx, dataSQL, repo, anchored, req.Limit, req.Offset)
		if err != nil {
			return nil, fmt.Errorf("doc glob query: %w", err)
		}
	} else {
		allSQL, err := DocGlobDataAllSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		rows, err = d.pool.Query(ctx, allSQL, repo, anchored, req.Offset)
		if err != nil {
			return nil, fmt.Errorf("doc glob query: %w", err)
		}
	}
	defer rows.Close()

	var results []store.GlobResult
	for rows.Next() {
		var filePath string
		var size int64
		var mtime time.Time
		if err := rows.Scan(&filePath, &size, &mtime); err != nil {
			return nil, fmt.Errorf("doc glob scan: %w", err)
		}
		// Normalize: strip leading / so output matches memory adapter.
		results = append(results, store.GlobResult{
			Path:    strings.TrimPrefix(filePath, "/"),
			Size:    size,
			ModTime: mtime.UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc glob rows: %w", err)
	}

	return &store.GlobResponse{Results: results, Total: total}, nil
}

func (d *DocAdapter) Grep(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	tree, err := d.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	// Verify root exists.
	if _, err := tree.Stat(req.Path); err != nil {
		return nil, err
	}

	prefix := normalizePrefix(req.Path)
	query, err := DocStreamGrepSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	rows, err := d.pool.Query(ctx, query, repo, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("doc grep query: %w", err)
	}
	defer rows.Close()

	// Compile regex if needed.
	var re *regexp.Regexp
	if req.Regex {
		pattern := req.Pattern
		if req.CaseInsensitive {
			pattern = "(?i)" + pattern
		}
		if req.WholeWord {
			pattern = `\b` + pattern + `\b`
		}
		if req.WholeLine {
			pattern = "^" + pattern + "$"
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		re = compiled
	}

	var matches []store.Match
	for rows.Next() {
		var filePath, content string
		if err := rows.Scan(&filePath, &content); err != nil {
			return nil, fmt.Errorf("doc grep scan: %w", err)
		}

		// Hidden filter.
		if !req.All && pathHasHidden(filePath, prefix) {
			continue
		}

		// Include/exclude glob.
		if !globMatch(filePath, req.Include, req.Exclude) {
			continue
		}

		// Line-level matching.
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if grepLineMatch(line, req.Pattern, re, req.CaseInsensitive, req.WholeWord, req.WholeLine) == req.Invert {
				continue
			}

			m := store.Match{
				Path: filePath,
				Line: i + 1,
				Text: line,
			}
			if req.ContextBefore > 0 {
				start := i - req.ContextBefore
				if start < 0 {
					start = 0
				}
				m.Before = copyLines(lines[start:i])
			}
			if req.ContextAfter > 0 {
				end := i + 1 + req.ContextAfter
				if end > len(lines) {
					end = len(lines)
				}
				m.After = copyLines(lines[i+1 : end])
			}
			matches = append(matches, m)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc grep rows: %w", err)
	}

	return &store.GrepResponse{Matches: matches}, nil
}

func (d *DocAdapter) Tree(ctx context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	tree, err := d.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	text, err := tree.Tree(req.Path, req.Depth, vfs.TreeOptions{
		All:       req.All,
		DirsOnly:  req.DirsOnly,
		FullPath:  req.FullPath,
		ShowSize:  req.ShowSize,
		Sort:      req.Sort,
		DirsFirst: req.DirsFirst,
	})
	if err != nil {
		return nil, err
	}

	// Use Stat for Root node to match old adapter behavior exactly.
	root, err := tree.Stat(req.Path)
	if err != nil {
		return nil, err
	}

	return &store.TreeResponse{
		Root: root,
		Text: text,
	}, nil
}

// --- Write methods ---

func (d *DocAdapter) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	if req.Path == "/" {
		return nil, fmt.Errorf("doc put: %w", store.ErrIsDir)
	}

	size := int64(len(req.Content))
	hash := store.HashContent(req.Content)
	title := path.Base(req.Path)

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("doc put begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Check if path already exists (with hash for CAS).
	lookupSQL, err := DocLookupPathWithHashSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	var existingDocID pgtype.UUID
	var existingHash string
	err = tx.QueryRow(ctx, lookupSQL, repo, req.Path).Scan(&existingDocID, &existingHash)

	if err == nil {
		// Path exists — CAS check for update.
		if req.ExpectedHash != "" && req.ExpectedHash != "*" && req.ExpectedHash != existingHash {
			return nil, fmt.Errorf("doc put cas: %w (expected %s, got %s)", store.ErrConflict, req.ExpectedHash, existingHash)
		}
		// Create-only requested but path already exists.
		if req.ExpectedHash == "*" {
			return nil, fmt.Errorf("doc put create-only: %w (path already exists)", store.ErrConflict)
		}
		// Update the doc in-place to avoid orphans.
		updateSQL, err := DocUpdateByPathSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, updateSQL, repo, req.Path, req.Content, hash, title); err != nil {
			return nil, fmt.Errorf("doc put update: %w", err)
		}
		// Update bound path size/mtime.
		upsertPathSQL, err := DocUpsertPathSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, upsertPathSQL, repo, req.Path, existingDocID, size); err != nil {
			return nil, fmt.Errorf("doc put path update: %w", err)
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		// New file — insert doc + bound path.
		insertSQL, err := DocInsertSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		var docID pgtype.UUID
		if err := tx.QueryRow(ctx, insertSQL, title, req.Content, hash).Scan(&docID); err != nil {
			return nil, fmt.Errorf("doc put insert: %w", err)
		}

		upsertPathSQL, err := DocUpsertPathSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, upsertPathSQL, repo, req.Path, docID, size); err != nil {
			return nil, fmt.Errorf("doc put path: %w", err)
		}
	} else {
		return nil, fmt.Errorf("doc put lookup: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("doc put commit: %w", err)
	}

	d.invalidateRepo(repo)

	return &store.PutResponse{
		Node: store.Node{
			Path:    req.Path,
			Name:    path.Base(req.Path),
			Kind:    "file",
			Size:    size,
			ModTime: time.Now().UTC().Format(time.RFC3339),
			Hash:    hash,
		},
	}, nil
}

func (d *DocAdapter) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	if req.Path == "/" {
		return nil, store.ErrCannotDeleteRoot
	}

	// CAS check for single file deletes.
	if req.ExpectedHash != "" {
		lookupSQL, err := DocLookupPathWithHashSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		var docID pgtype.UUID
		var currentHash string
		if err := d.pool.QueryRow(ctx, lookupSQL, repo, req.Path).Scan(&docID, &currentHash); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("doc delete %s: %w", req.Path, store.ErrNotFound)
			}
			return nil, fmt.Errorf("doc delete lookup: %w", err)
		}
		if currentHash != req.ExpectedHash {
			return nil, fmt.Errorf("doc delete cas: %w (expected %s, got %s)", store.ErrConflict, req.ExpectedHash, currentHash)
		}
	}

	tree, err := d.treeFor(ctx, repo)
	if err != nil {
		return nil, err
	}

	// Check if path exists (file or implicit dir).
	stat, err := tree.Stat(req.Path)
	if err != nil {
		return nil, fmt.Errorf("doc delete: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("doc delete begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if stat.Kind == "dir" {
		// Recursive delete: delete path + all descendants.
		deleteSQL, err := DocDeletePathRecursiveSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, deleteSQL, repo, req.Path)
		if err != nil {
			return nil, fmt.Errorf("doc delete dir: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil, fmt.Errorf("doc delete %s: %w", req.Path, store.ErrNotFound)
		}
	} else {
		// Single file delete.
		deleteSQL, err := DocDeletePathSQL(d.cfg)
		if err != nil {
			return nil, err
		}
		tag, err := tx.Exec(ctx, deleteSQL, repo, req.Path)
		if err != nil {
			return nil, fmt.Errorf("doc delete file: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil, fmt.Errorf("doc delete %s: %w", req.Path, store.ErrNotFound)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("doc delete commit: %w", err)
	}

	d.invalidateRepo(repo)

	return &store.DeleteResponse{}, nil
}

func (d *DocAdapter) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	if req.Path == "/" {
		return nil, fmt.Errorf("doc edit: %w", store.ErrIsDir)
	}
	if req.Old == "" {
		return nil, store.ErrEmptyOld
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("doc edit begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the doc row for read-modify-write.
	selectSQL, err := DocSelectForUpdateSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	var docID pgtype.UUID
	var content string
	var currentHash string
	if err := tx.QueryRow(ctx, selectSQL, repo, req.Path).Scan(&docID, &content, &currentHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("doc edit %s: %w", req.Path, store.ErrNotFound)
		}
		return nil, fmt.Errorf("doc edit select: %w", err)
	}

	// CAS check.
	if req.ExpectedHash != "" && req.ExpectedHash != currentHash {
		return nil, fmt.Errorf("doc edit cas: %w (expected %s, got %s)", store.ErrConflict, req.ExpectedHash, currentHash)
	}

	// Perform string replacement (matching vfs.Tree.Edit semantics).
	var replaced int
	if req.All {
		replaced = strings.Count(content, req.Old)
	} else {
		replaced = strings.Count(content, req.Old)
		if replaced == 0 {
			return nil, store.ErrOldNotFound
		}
		replaced = 1
	}
	if replaced == 0 {
		return nil, store.ErrOldNotFound
	}

	var newContent string
	if req.All {
		newContent = strings.ReplaceAll(content, req.Old, req.New)
	} else {
		newContent = strings.Replace(content, req.Old, req.New, 1)
	}

	// Update doc content.
	hash := store.HashContent(newContent)
	updateSQL, err := DocUpdateByIDSQL(d.cfg)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, updateSQL, docID, newContent, hash); err != nil {
		return nil, fmt.Errorf("doc edit update: %w", err)
	}

	// Update bound path size/mtime.
	upsertPathSQL, err := DocUpsertPathSQL(d.cfg)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, upsertPathSQL, repo, req.Path, docID, int64(len(newContent))); err != nil {
		return nil, fmt.Errorf("doc edit path: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("doc edit commit: %w", err)
	}

	d.invalidateRepo(repo)

	return &store.EditResponse{
		Path:     req.Path,
		Replaced: replaced,
		Content:  newContent,
	}, nil
}

// --- Helpers ---

// normalizePrefix ensures a path ends with "/" for LIKE prefix queries.
func normalizePrefix(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "/"
	}
	return p + "/"
}
