package postgres

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gxfs/internal/store"
)

// pathEntry is a file path with metadata, used for LS/Tree derivation.
type pathEntry struct {
	path    string
	size    int64
	modTime time.Time
}

// DocAdapter implements store.Adapter as a read-only query layer over the
// document-centric tables (gxfs_docs, gxfs_repo_paths). Write methods
// (Put, Delete, Edit) return ErrReadOnlyMount.
//
// Directories are implicit: derived from file path prefixes, not stored.
// This matches the vfs.Tree behavior of the path-centric adapter.
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

// --- Read methods ---

func (d *DocAdapter) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	repo := d.repo(req.Repo)
	prefix := normalizePrefix(req.Path)

	query, err := DocListPathsSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	rows, err := d.pool.Query(ctx, query, repo, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("doc ls query: %w", err)
	}
	defer rows.Close()

	var allPaths []pathEntry
	for rows.Next() {
		var r pathEntry
		if err := rows.Scan(&r.path, &r.size, &r.modTime); err != nil {
			return nil, fmt.Errorf("doc ls scan: %w", err)
		}
		allPaths = append(allPaths, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc ls rows: %w", err)
	}

	// Build size/mtime lookup.
	infoMap := make(map[string]pathEntry, len(allPaths))
	for _, r := range allPaths {
		infoMap[r.path] = r
	}

	// Derive LS entries from file paths: dirs are implicit.
	nodes := deriveLSEntries(allPaths, req.Path, req.Recursive, req.All)

	// Sort.
	sortNodes(nodes, req.Sort, req.Reverse)

	total := len(nodes)
	nodes = paginateNodes(nodes, req.Limit, req.Offset)

	return &store.LSResponse{Nodes: nodes, Total: total}, nil
}

func (d *DocAdapter) Cat(ctx context.Context, req store.CatRequest) (*store.CatResponse, error) {
	repo := d.repo(req.Repo)

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

	// Try file first.
	fileQuery, err := DocStatSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	var filePath string
	var size int64
	var mtime time.Time
	var hash string
	err = d.pool.QueryRow(ctx, fileQuery, repo, req.Path).Scan(&filePath, &size, &mtime, &hash)
	if err == nil {
		return &store.StatResponse{
			Node: store.Node{
				Path:    filePath,
				Name:    path.Base(filePath),
				Kind:    "file",
				Size:    size,
				ModTime: mtime.UTC().Format(time.RFC3339),
				Hash:    hash,
			},
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("doc stat %s: %w", req.Path, err)
	}

	// Not a file — check if it's an implicit directory.
	dirQuery, err := DocStatDirSQL(d.cfg)
	if err != nil {
		return nil, err
	}
	prefix := normalizePrefix(req.Path)
	var count int
	if err := d.pool.QueryRow(ctx, dirQuery, repo, prefix+"%").Scan(&count); err != nil {
		return nil, fmt.Errorf("doc stat dir %s: %w", req.Path, err)
	}
	if count == 0 {
		return nil, fmt.Errorf("doc stat %s: %w", req.Path, store.ErrNotFound)
	}

	return &store.StatResponse{
		Node: store.Node{
			Path: req.Path,
			Name: path.Base(req.Path),
			Kind: "dir",
		},
	}, nil
}

func (d *DocAdapter) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	repo := d.repo(req.Repo)
	prefix := normalizePrefix(req.Path)

	query, err := DocListPathsSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	rows, err := d.pool.Query(ctx, query, repo, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("doc find query: %w", err)
	}
	defer rows.Close()

	var nodes []store.Node
	for rows.Next() {
		var filePath string
		var size int64
		var mtime time.Time
		if err := rows.Scan(&filePath, &size, &mtime); err != nil {
			return nil, fmt.Errorf("doc find scan: %w", err)
		}

		// Hidden filter.
		if !req.All && pathHasHidden(filePath, prefix) {
			continue
		}

		// Name filter.
		if req.Name != "" && path.Base(filePath) != req.Name {
			continue
		}
		if req.IName != "" && !strings.EqualFold(path.Base(filePath), req.IName) {
			continue
		}

		// Type filter — we only have files.
		if req.Type == "dir" {
			continue
		}

		// Depth filter.
		depth := pathDepth(filePath, req.Path)
		if req.MaxDepth > 0 && depth > req.MaxDepth {
			continue
		}
		if req.MinDepth > 0 && depth < req.MinDepth {
			continue
		}

		nodes = append(nodes, store.Node{
			Path:    filePath,
			Name:    path.Base(filePath),
			Kind:    "file",
			Size:    size,
			ModTime: mtime.UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc find rows: %w", err)
	}

	total := len(nodes)
	nodes = paginateNodes(nodes, req.Limit, req.Offset)

	return &store.FindResponse{Nodes: nodes, Total: total}, nil
}

func (d *DocAdapter) Search(ctx context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	repo := d.repo(req.Repo)

	pathFilter := strings.TrimRight(req.Path, "/")
	if pathFilter == "" || pathFilter == "/" {
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

	pathFilter := strings.TrimRight(req.Path, "/")
	if pathFilter == "" || pathFilter == "/" {
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
	prefix := normalizePrefix(req.Path)

	// Verify the root path exists.
	if _, err := d.Stat(ctx, store.StatRequest{Repo: repo, Path: req.Path}); err != nil {
		return nil, err
	}

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
	prefix := normalizePrefix(req.Path)

	query, err := DocListPathsSQL(d.cfg)
	if err != nil {
		return nil, err
	}

	rows, err := d.pool.Query(ctx, query, repo, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("doc tree query: %w", err)
	}
	defer rows.Close()

	var allPaths []pathEntry
	for rows.Next() {
		var r pathEntry
		if err := rows.Scan(&r.path, &r.size, &r.modTime); err != nil {
			return nil, fmt.Errorf("doc tree scan: %w", err)
		}
		allPaths = append(allPaths, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doc tree rows: %w", err)
	}

	if len(allPaths) == 0 {
		return nil, fmt.Errorf("doc tree %s: %w", req.Path, store.ErrNotFound)
	}

	// Build tree text.
	var b strings.Builder
	buildTreeText(&b, allPaths, req.Path, req.Depth, req.All, req.DirsOnly, req.FullPath, req.ShowSize, req.Sort, req.DirsFirst)
	text := b.String()

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

// deriveLSEntries builds Node entries from flat file paths, deriving implicit
// directories from path prefixes. Matches vfs.Tree.LS behavior.
func deriveLSEntries(paths []pathEntry, root string, recursive, all bool) []store.Node {
	prefix := strings.TrimRight(root, "/") + "/"
	if root == "/" || root == "" {
		prefix = "/"
	}

	seen := make(map[string]bool)
	var nodes []store.Node

	for _, p := range paths {
		if !strings.HasPrefix(p.path, prefix) {
			continue
		}

		rel := strings.TrimPrefix(p.path, prefix)
		parts := strings.SplitN(rel, "/", 2)

		if len(parts) == 1 {
			// File is direct child.
			name := parts[0]
			if !all && strings.HasPrefix(name, ".") {
				continue
			}
			if !seen[p.path] {
				nodes = append(nodes, store.Node{
					Path:    p.path,
					Name:    name,
					Kind:    "file",
					Size:    p.size,
					ModTime: p.modTime.UTC().Format(time.RFC3339),
				})
				seen[p.path] = true
			}
		} else {
			// File is in a subdirectory — emit the directory.
			dirName := parts[0]
			dirPath := prefix + dirName
			if prefix == "/" {
				dirPath = "/" + dirName
			}
			if !all && strings.HasPrefix(dirName, ".") {
				if !recursive {
					continue
				}
				// In recursive mode, still need to descend into hidden dirs
				// but only if all=true. Skip hidden dirs in non-all mode.
				continue
			}
			if !seen[dirPath] {
				nodes = append(nodes, store.Node{
					Path: dirPath,
					Name: dirName,
					Kind: "dir",
				})
				seen[dirPath] = true
			}

			// In recursive mode, also emit the file.
			if recursive {
				if !all && pathHasHidden(p.path, prefix) {
					continue
				}
				if !seen[p.path] {
					nodes = append(nodes, store.Node{
						Path:    p.path,
						Name:    path.Base(p.path),
						Kind:    "file",
						Size:    p.size,
						ModTime: p.modTime.UTC().Format(time.RFC3339),
					})
					seen[p.path] = true
				}
			}
		}
	}

	return nodes
}

// sortNodes sorts nodes by the given field. Mirrors vfs.Tree sort behavior.
func sortNodes(nodes []store.Node, sortBy string, reverse bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		var less bool
		switch sortBy {
		case "size":
			less = nodes[i].Size < nodes[j].Size
		case "mtime":
			less = nodes[i].ModTime < nodes[j].ModTime
		default:
			less = nodes[i].Name < nodes[j].Name
		}
		if reverse {
			return !less
		}
		return less
	})
}

// pathDepth returns the depth of filePath relative to root.
func pathDepth(filePath, root string) int {
	rel := strings.TrimPrefix(filePath, strings.TrimRight(root, "/")+"/")
	return strings.Count(rel, "/")
}

// buildTreeText generates tree-style output text from paths.
func buildTreeText(b *strings.Builder, paths []pathEntry, root string, depth int, all, dirsOnly, fullPath, showSize bool, sortBy string, dirsFirst bool) {
	prefix := normalizePrefix(root)

	// Group by immediate child.
	dirs := make(map[string][]pathEntry)
	for _, p := range paths {
		if !strings.HasPrefix(p.path, prefix) {
			continue
		}
		rel := strings.TrimPrefix(p.path, prefix)
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) == 1 {
			child := prefix + parts[0]
			dirs[child] = append(dirs[child], p)
		} else {
			child := prefix + parts[0]
			if dirs[child] == nil {
				dirs[child] = []pathEntry{}
			}
		}
	}

	// Collect children.
	var children []string
	for child := range dirs {
		name := path.Base(child)
		if !all && strings.HasPrefix(name, ".") {
			continue
		}
		children = append(children, child)
	}
	sort.Strings(children)

	for _, child := range children {
		name := path.Base(child)
		if fullPath {
			name = child
		}

		// Check if this child is a directory (has sub-entries).
		isDir := false
		for _, p := range paths {
			if strings.HasPrefix(p.path, child+"/") {
				isDir = true
				break
			}
		}

		if isDir {
			fmt.Fprintf(b, "%s/\n", name)
			if depth == 0 || depth > 1 {
				nextDepth := depth
				if depth > 1 {
					nextDepth = depth - 1
				}
				buildTreeText(b, paths, child, nextDepth, all, dirsOnly, fullPath, showSize, sortBy, dirsFirst)
			}
		} else if !dirsOnly {
			if showSize {
				// Find the size.
				var sz int64
				for _, p := range paths {
					if p.path == child {
						sz = p.size
						break
					}
				}
				fmt.Fprintf(b, "%s  [%d]\n", name, sz)
			} else {
				fmt.Fprintf(b, "%s\n", name)
			}
		}
	}
}
