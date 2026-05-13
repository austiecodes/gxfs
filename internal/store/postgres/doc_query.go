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
