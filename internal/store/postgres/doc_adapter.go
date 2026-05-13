package postgres

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gxfs/internal/store"
	"gxfs/internal/vfs"
)

// DocAdapter implements store.Adapter as a read-only query layer over the
// document-centric tables (gxfs_docs, gxfs_repo_paths). Write methods
// (Put, Delete, Edit) return ErrReadOnlyMount.
//
// Read methods fall into two categories:
//  1. Structure queries (LS, Find, Stat, Tree): build a vfs.Tree from
//     gxfs_repo_paths file entries, then delegate to vfs.Tree methods.
//     This guarantees exact behavioral compatibility with the old adapter.
//  2. Content queries (Cat, Search, BatchHashes, Grep): query gxfs_docs
//     directly for content, hashes, and full-text search.
type DocAdapter struct {
	pool *pgxpool.Pool
	cfg  Config
}

var _ store.Adapter = (*DocAdapter)(nil)

// NewDocAdapter creates a read-only adapter backed by document-centric tables.
func NewDocAdapter(pool *pgxpool.Pool, cfg Config) *DocAdapter {
	return &DocAdapter{pool: pool, cfg: cfg}
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

// buildTree loads all file paths for a repo from gxfs_repo_paths and builds
// a vfs.Tree. This mirrors the old adapter's loadTree pattern, but reads from
// the new document-centric tables instead of vfs_nodes.
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

// --- Read methods ---

func (d *DocAdapter) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	tree, err := d.buildTree(ctx, repo)
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

	tree, err := d.buildTree(ctx, repo)
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

	tree, err := d.buildTree(ctx, repo)
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

func (d *DocAdapter) Grep(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	repo := d.repo(req.Repo)
	req.Path = cleanDocPath(req.Path)

	tree, err := d.buildTree(ctx, repo)
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

	tree, err := d.buildTree(ctx, repo)
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

	name := path.Base(req.Path)
	if req.Path == "/" {
		name = "."
	}

	return &store.TreeResponse{
		Root: store.Node{
			Path: req.Path,
			Name: name,
			Kind: "dir",
		},
		Text: text,
	}, nil
}

// --- Write methods (read-only) ---

func (d *DocAdapter) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	return nil, store.ErrReadOnlyMount
}

func (d *DocAdapter) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	return nil, store.ErrReadOnlyMount
}

func (d *DocAdapter) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	return nil, store.ErrReadOnlyMount
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
