package postgres

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/austiecodes/gxfs/internal/store"
)

// BackfillResult reports the outcome of a document table backfill.
type BackfillResult struct {
	DocsInserted   int // total docs upserted into gxfs_docs
	PathsInserted  int // total repo_paths upserted into gxfs_repo_paths
	HashesComputed int // docs where content_hash was NULL and had to be computed
}

// BackfillDocs migrates all file data from the legacy path-centric tables
// (vfs_nodes, vfs_content, vfs_repo_nodes) into the new document-centric
// tables (gxfs_docs, gxfs_repo_paths).
//
// The operation is idempotent: running it multiple times produces the same
// result thanks to legacy_path UNIQUE on gxfs_docs and (repo, path) PRIMARY
// KEY on gxfs_repo_paths.
//
// Each logical file gets its own doc row (no content dedup). Directories are
// not migrated — they are implicit from path prefixes in the new schema.
// If content_hash is NULL in the old table, it is computed from content using
// store.HashContent.
func BackfillDocs(ctx context.Context, pool *pgxpool.Pool, cfg Config) (*BackfillResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin backfill tx: %w", err)
	}
	defer tx.Rollback(ctx)

	srcQuery, err := backfillSourceSQL(cfg)
	if err != nil {
		return nil, fmt.Errorf("build source query: %w", err)
	}

	docInsert, err := backfillDocInsertSQL(cfg)
	if err != nil {
		return nil, fmt.Errorf("build doc insert query: %w", err)
	}

	pathInsert, err := backfillPathInsertSQL(cfg)
	if err != nil {
		return nil, fmt.Errorf("build path insert query: %w", err)
	}

	rows, err := tx.Query(ctx, srcQuery)
	if err != nil {
		return nil, fmt.Errorf("query source data: %w", err)
	}

	// Collect all source rows first so we can close the result set before
	// issuing INSERTs on the same single-connection transaction.
	type sourceRow struct {
		repo         string
		filePath     string
		content      string
		hash         string
		size         int64
		mtime        time.Time
		hashComputed bool
	}
	var sources []sourceRow
	for rows.Next() {
		var repo, filePath, content string
		var contentHash *string
		var size int64
		var mtime pgtype.Timestamptz

		if err := rows.Scan(&repo, &filePath, &content, &contentHash, &size, &mtime); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan source row: %w", err)
		}

		hash := ""
		hashComputed := false
		if contentHash != nil && *contentHash != "" {
			hash = *contentHash
		} else {
			hash = store.HashContent(content)
			hashComputed = true
		}

		mtimeVal := time.Now()
		if mtime.Valid {
			mtimeVal = mtime.Time
		}

		sources = append(sources, sourceRow{
			repo:         repo,
			filePath:     filePath,
			content:      content,
			hash:         hash,
			size:         size,
			mtime:        mtimeVal,
			hashComputed: hashComputed,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read source rows: %w", err)
	}

	var result BackfillResult
	docIDs := make(map[string]pgtype.UUID) // legacy_path → doc_id

	for _, src := range sources {
		// Insert doc once per unique path (idempotent via legacy_path UNIQUE).
		docID, seen := docIDs[src.filePath]
		if !seen {
			title := path.Base(src.filePath)
			if err := tx.QueryRow(ctx, docInsert, src.filePath, title, src.content, src.hash).Scan(&docID); err != nil {
				return nil, fmt.Errorf("insert doc for %s: %w", src.filePath, err)
			}
			docIDs[src.filePath] = docID
			result.DocsInserted++

			if src.hashComputed {
				result.HashesComputed++
			}
		}

		// Insert repo_path (idempotent via PK).
		if _, err := tx.Exec(ctx, pathInsert, src.repo, src.filePath, docID, src.size, src.mtime); err != nil {
			return nil, fmt.Errorf("insert repo_path %s/%s: %w", src.repo, src.filePath, err)
		}
		result.PathsInserted++
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit backfill: %w", err)
	}

	return &result, nil
}

// backfillSourceSQL returns a query that selects all file data from the old
// path-centric tables: (repo, path, content, content_hash, size, mtime).
func backfillSourceSQL(cfg Config) (string, error) {
	nodesTable, err := quoteTable(cfg.Schema, cfg.NodesTable)
	if err != nil {
		return "", err
	}
	contentTable, err := quoteTable(cfg.Schema, cfg.ContentTable)
	if err != nil {
		return "", err
	}
	repoNodesTable, err := quoteTable(cfg.Schema, cfg.RepoNodesTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	kindCol, err := quoteIdent(cfg.Files.KindColumn)
	if err != nil {
		return "", fmt.Errorf("kind column: %w", err)
	}

	sizeExpr := "0"
	if cfg.Files.SizeColumn != "" {
		sizeCol, err := quoteIdent(cfg.Files.SizeColumn)
		if err != nil {
			return "", fmt.Errorf("size column: %w", err)
		}
		sizeExpr = "n." + sizeCol
	}

	mtimeExpr := "now()"
	if cfg.Files.MTimeColumn != "" {
		mtimeCol, err := quoteIdent(cfg.Files.MTimeColumn)
		if err != nil {
			return "", fmt.Errorf("mtime column: %w", err)
		}
		mtimeExpr = "n." + mtimeCol
	}

	return fmt.Sprintf(
		"select r.repo, n.%s, c.content, c.content_hash, %s, %s "+
			"from %s n "+
			"join %s c on n.%s = c.%s "+
			"join %s r on n.%s = r.%s "+
			"where n.%s = 'file' "+
			"order by n.%s",
		pathCol, sizeExpr, mtimeExpr,
		nodesTable,
		contentTable, pathCol, pathCol,
		repoNodesTable, pathCol, pathCol,
		kindCol, pathCol,
	), nil
}

// backfillDocInsertSQL returns a query that inserts a doc row and returns its
// UUID. Idempotent via legacy_path partial unique index: on conflict, updates
// title, content, content_hash, and updated_at to keep all columns consistent
// across re-runs.
// Revision stays at 1 for all import snapshots — backfill is defined as a
// "latest import snapshot", not a user edit. Revision tracking begins when
// production writes go through the new doc adapter (post Phase 1A).
func backfillDocInsertSQL(cfg Config) (string, error) {
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"insert into %s(legacy_path, title, content, content_hash) "+
			"values($1, $2, $3, $4) "+
			"on conflict(legacy_path) where legacy_path is not null "+
			"do update set title = excluded.title, content = excluded.content, "+
			"content_hash = excluded.content_hash, updated_at = now() "+
			"returning id",
		docsTable,
	), nil
}

// backfillPathInsertSQL returns a query that inserts a repo_path row.
// Idempotent via (repo, path) PRIMARY KEY: on conflict, updates doc_id, size,
// and mtime.
func backfillPathInsertSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"insert into %s(repo, path, doc_id, size, mtime) "+
			"values($1, $2, $3, $4, $5) "+
			"on conflict(repo, path) do update set "+
			"doc_id = excluded.doc_id, size = excluded.size, mtime = excluded.mtime",
		pathsTable,
	), nil
}
