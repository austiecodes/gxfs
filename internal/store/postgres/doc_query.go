package postgres

import (
	"fmt"
)

// DocListPathsSQL returns a query that selects all file paths under a prefix
// from the document-centric tables.
func DocListPathsSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select path, size, mtime from %s where repo = $1 and path like $2 order by path",
		pathsTable,
	), nil
}

// DocCatSQL returns a query that selects content and hash for a single file.
func DocCatSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select d.content, d.content_hash from %s rp join %s d on rp.doc_id = d.id where rp.repo = $1 and rp.path = $2",
		pathsTable, docsTable,
	), nil
}

// DocStatSQL returns a query that selects metadata for a single path.
func DocStatSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select rp.path, rp.size, rp.mtime, d.content_hash from %s rp join %s d on rp.doc_id = d.id where rp.repo = $1 and rp.path = $2",
		pathsTable, docsTable,
	), nil
}

// DocStatDirSQL returns a query that checks if any file exists under a directory
// prefix, used to determine if an implicit directory exists.
func DocStatDirSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select count(*) from %s where repo = $1 and path like $2",
		pathsTable,
	), nil
}

// DocSearchCountSQL returns a query that counts full-text search results.
func DocSearchCountSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select count(*) from %s rp join %s d on rp.doc_id = d.id, "+
			"plainto_tsquery('english', $2) as query "+
			"where rp.repo = $1 and d.content_search @@ query "+
			"and ($3 = '' or rp.path = $3 or rp.path like $3 || '/%%%%')",
		pathsTable, docsTable,
	), nil
}

// DocSearchDataSQL returns a query that selects full-text search results with
// rank and snippet.
func DocSearchDataSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select rp.path, ts_rank_cd(d.content_search, query, 32) as rank, "+
			"ts_headline('english', d.content, query, 'StartSel=**,StopSel=**,MaxWords=50,MinWords=10') as snippet, "+
			"rp.size, rp.mtime "+
			"from %s rp join %s d on rp.doc_id = d.id, "+
			"plainto_tsquery('english', $2) as query "+
			"where rp.repo = $1 and d.content_search @@ query "+
			"and ($3 = '' or rp.path = $3 or rp.path like $3 || '/%%%%') "+
			"order by rank desc limit $4 offset $5",
		pathsTable, docsTable,
	), nil
}

// DocBatchHashesSQL returns a query that selects content hashes for all files
// under a prefix.
func DocBatchHashesSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select rp.path, d.content_hash from %s rp join %s d on rp.doc_id = d.id "+
			"where rp.repo = $1 and d.content_hash is not null "+
			"and ($2 = '' or rp.path = $2 or rp.path like $2 || '/%%%%') "+
			"order by rp.path",
		pathsTable, docsTable,
	), nil
}

// DocStreamGrepSQL returns a query that streams (path, content) for grep.
func DocStreamGrepSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select rp.path, d.content from %s rp join %s d on rp.doc_id = d.id "+
			"where rp.repo = $1 and rp.path like $2 "+
			"order by rp.path",
		pathsTable, docsTable,
	), nil
}

// --- Write SQL builders ---

// DocInsertSQL inserts a new doc row with content and hash, returning the doc ID.
// content_search is GENERATED and auto-updated.
func DocInsertSQL(cfg Config) (string, error) {
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"insert into %s(title, content, content_hash) values($1, $2, $3) returning id",
		docsTable,
	), nil
}

// DocUpdateByPathSQL updates the doc linked to a specific repo_path.
// Used when Put overwrites an existing file — updates in-place to avoid orphans.
// content_search is GENERATED and auto-updated.
func DocUpdateByPathSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"update %s d set content = $3, content_hash = $4, "+
			"title = $5, revision = revision + 1, updated_at = now() "+
			"from %s rp where rp.repo = $1 and rp.path = $2 and rp.doc_id = d.id",
		docsTable, pathsTable,
	), nil
}

// DocSelectForUpdateSQL locks the doc row for Edit's read-modify-write cycle.
func DocSelectForUpdateSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select d.id, d.content from %s rp join %s d on rp.doc_id = d.id "+
			"where rp.repo = $1 and rp.path = $2 for update of d",
		pathsTable, docsTable,
	), nil
}

// DocUpdateByIDSQL updates a doc by its ID (used after FOR UPDATE).
func DocUpdateByIDSQL(cfg Config) (string, error) {
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"update %s set content = $2, content_hash = $3, revision = revision + 1, updated_at = now() "+
			"where id = $1",
		docsTable,
	), nil
}

// DocUpsertPathSQL inserts or updates a repo_path row.
func DocUpsertPathSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"insert into %s(repo, path, doc_id, size, mtime) values($1, $2, $3, $4, now()) "+
			"on conflict(repo, path) do update set doc_id = excluded.doc_id, "+
			"size = excluded.size, mtime = excluded.mtime",
		pathsTable,
	), nil
}

// DocLookupPathSQL checks if a repo_path exists and returns the doc_id.
func DocLookupPathSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select doc_id from %s where repo = $1 and path = $2",
		pathsTable,
	), nil
}

// DocDeletePathSQL deletes a single repo_path row.
func DocDeletePathSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"delete from %s where repo = $1 and path = $2",
		pathsTable,
	), nil
}

// DocDeletePathRecursiveSQL deletes all repo_path rows under a prefix.
func DocDeletePathRecursiveSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"delete from %s where repo = $1 and (path = $2 or path like $2 || '/%%%%')",
		pathsTable,
	), nil
}

// DocGlobCountSQL returns a query that counts paths matching a regex pattern.
func DocGlobCountSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select count(*) from %s where repo = $1 and path ~ $2",
		pathsTable,
	), nil
}

// DocGlobDataSQL returns a query that selects paths matching a regex pattern
// with pagination (LIMIT $3 OFFSET $4).
func DocGlobDataSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select path, size, mtime from %s where repo = $1 and path ~ $2 order by path limit $3 offset $4",
		pathsTable,
	), nil
}

// DocGlobDataAllSQL returns a query that selects all paths matching a regex pattern
// without LIMIT (unlimited results, offset only).
func DocGlobDataAllSQL(cfg Config) (string, error) {
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select path, size, mtime from %s where repo = $1 and path ~ $2 order by path",
		pathsTable,
	), nil
}
