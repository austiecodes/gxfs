package postgres

import (
	"fmt"
)

const (
	docRepoPathsTable       = "gxfs_repo_paths"
	docRepoScopeColumn      = "repo"
	docNamespacePathsTable  = "gxfs_doc_namespace_paths"
	docNamespaceScopeColumn = "namespace"
)

type docBindingSQL struct {
	pathsTable  string
	scopeColumn string
}

func docBindingForConfig(cfg Config) (docBindingSQL, error) {
	pathsTable := cfg.DocBinding.PathsTable
	scopeColumn := cfg.DocBinding.ScopeColumn
	if pathsTable == "" && scopeColumn == "" {
		pathsTable = docRepoPathsTable
		scopeColumn = docRepoScopeColumn
	} else if pathsTable == "" || scopeColumn == "" {
		return docBindingSQL{}, fmt.Errorf("doc binding requires both paths table and scope column")
	}

	quotedPathsTable, err := quoteTable(cfg.Schema, pathsTable)
	if err != nil {
		return docBindingSQL{}, err
	}
	if !identPattern.MatchString(scopeColumn) {
		return docBindingSQL{}, fmt.Errorf("scope column: unsafe identifier %q", scopeColumn)
	}
	return docBindingSQL{pathsTable: quotedPathsTable, scopeColumn: scopeColumn}, nil
}

// DocListPathsSQL returns a query that selects all file paths under a prefix
// from the document-centric tables.
func DocListPathsSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select path, size, mtime from %s where %s = $1 and path like $2 order by path",
		binding.pathsTable, binding.scopeColumn,
	), nil
}

// DocCatSQL returns a query that selects content and hash for a single file.
func DocCatSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select d.content, d.content_hash from %s rp join %s d on rp.doc_id = d.id where rp.%s = $1 and rp.path = $2",
		binding.pathsTable, docsTable, binding.scopeColumn,
	), nil
}

// DocStatSQL returns a query that selects metadata for a single path.
func DocStatSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select rp.path, rp.size, rp.mtime, d.content_hash from %s rp join %s d on rp.doc_id = d.id where rp.%s = $1 and rp.path = $2",
		binding.pathsTable, docsTable, binding.scopeColumn,
	), nil
}

// DocStatDirSQL returns a query that checks if any file exists under a directory
// prefix, used to determine if an implicit directory exists.
func DocStatDirSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select count(*) from %s where %s = $1 and path like $2",
		binding.pathsTable, binding.scopeColumn,
	), nil
}

// DocSearchCountSQL returns a query that counts full-text search results.
func DocSearchCountSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
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
			"where rp.%s = $1 and d.content_search @@ query "+
			"and ($3 = '' or rp.path = $3 or rp.path like $3 || '/%%%%')",
		binding.pathsTable, docsTable, binding.scopeColumn,
	), nil
}

// DocSearchDataSQL returns a query that selects full-text search results with
// rank and snippet.
func DocSearchDataSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
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
			"where rp.%s = $1 and d.content_search @@ query "+
			"and ($3 = '' or rp.path = $3 or rp.path like $3 || '/%%%%') "+
			"order by rank desc limit $4 offset $5",
		binding.pathsTable, docsTable, binding.scopeColumn,
	), nil
}

// DocLocateCountSQL returns a query that counts documents matching a full-text query.
// This is used by Locate for discovery-level search across the entire repo.
func DocLocateCountSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
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
			"where rp.%s = $1 and d.content_search @@ query",
		binding.pathsTable, docsTable, binding.scopeColumn,
	), nil
}

// DocLocateDataSQL returns a query that selects locate results with rank and snippet.
// Locate is designed for discovery - it searches the entire repo and returns
// document-level results with lexical ranking via ts_rank_cd.
func DocLocateDataSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select rp.path, ts_rank_cd(d.content_search, query, 32) as rank, "+
			"ts_headline('english', d.content, query, 'StartSel=**,StopSel=**,MaxWords=60,MinWords=15') as snippet "+
			"from %s rp join %s d on rp.doc_id = d.id, "+
			"plainto_tsquery('english', $2) as query "+
			"where rp.%s = $1 and d.content_search @@ query "+
			"order by rank desc limit $3 offset $4",
		binding.pathsTable, docsTable, binding.scopeColumn,
	), nil
}

// DocBatchHashesSQL returns a query that selects content hashes for all files
// under a prefix.
func DocBatchHashesSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select rp.path, d.content_hash from %s rp join %s d on rp.doc_id = d.id "+
			"where rp.%s = $1 and d.content_hash is not null "+
			"and ($2 = '' or rp.path = $2 or rp.path like $2 || '/%%%%') "+
			"order by rp.path",
		binding.pathsTable, docsTable, binding.scopeColumn,
	), nil
}

// DocStreamGrepSQL returns a query that streams (path, content) for grep.
func DocStreamGrepSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select rp.path, d.content from %s rp join %s d on rp.doc_id = d.id "+
			"where rp.%s = $1 and rp.path like $2 "+
			"order by rp.path",
		binding.pathsTable, docsTable, binding.scopeColumn,
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

// DocUpdateByPathSQL updates the doc linked to a specific bound path.
// Used when Put overwrites an existing file — updates in-place to avoid orphans.
// content_search is GENERATED and auto-updated.
func DocUpdateByPathSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
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
			"from %s rp where rp.%s = $1 and rp.path = $2 and rp.doc_id = d.id",
		docsTable, binding.pathsTable, binding.scopeColumn,
	), nil
}

// DocSelectForUpdateSQL locks the doc row for Edit's read-modify-write cycle.
func DocSelectForUpdateSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select d.id, d.content, d.content_hash from %s rp join %s d on rp.doc_id = d.id "+
			"where rp.%s = $1 and rp.path = $2 for update of d",
		binding.pathsTable, docsTable, binding.scopeColumn,
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

// DocUpsertPathSQL inserts or updates a bound path row.
func DocUpsertPathSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"insert into %s(%s, path, doc_id, size, mtime) values($1, $2, $3, $4, now()) "+
			"on conflict(%s, path) do update set doc_id = excluded.doc_id, "+
			"size = excluded.size, mtime = excluded.mtime",
		binding.pathsTable, binding.scopeColumn, binding.scopeColumn,
	), nil
}

// DocLookupPathSQL checks if a bound path exists and returns the doc_id.
func DocLookupPathSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select doc_id from %s where %s = $1 and path = $2",
		binding.pathsTable, binding.scopeColumn,
	), nil
}

// DocLookupPathWithHashSQL returns doc_id and content_hash for a bound path.
func DocLookupPathWithHashSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select p.doc_id, d.content_hash from %s p join %s d on p.doc_id = d.id where p.%s = $1 and p.path = $2",
		binding.pathsTable, docsTable, binding.scopeColumn,
	), nil
}

// DocDeletePathSQL deletes a single bound path row.
func DocDeletePathSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"delete from %s where %s = $1 and path = $2",
		binding.pathsTable, binding.scopeColumn,
	), nil
}

// DocDeletePathRecursiveSQL deletes all bound path rows under a prefix.
func DocDeletePathRecursiveSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"delete from %s where %s = $1 and (path = $2 or path like $2 || '/%%%%')",
		binding.pathsTable, binding.scopeColumn,
	), nil
}

// DocGlobCountSQL returns a query that counts paths matching a regex pattern.
func DocGlobCountSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select count(*) from %s where %s = $1 and path ~ $2",
		binding.pathsTable, binding.scopeColumn,
	), nil
}

// DocGlobDataSQL returns a query that selects paths matching a regex pattern
// with pagination (LIMIT $3 OFFSET $4).
func DocGlobDataSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select path, size, mtime from %s where %s = $1 and path ~ $2 order by path limit $3 offset $4",
		binding.pathsTable, binding.scopeColumn,
	), nil
}

// DocGlobDataAllSQL returns a query that selects all paths matching a regex pattern
// without LIMIT but with OFFSET support ($3).
func DocGlobDataAllSQL(cfg Config) (string, error) {
	binding, err := docBindingForConfig(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"select path, size, mtime from %s where %s = $1 and path ~ $2 order by path offset $3",
		binding.pathsTable, binding.scopeColumn,
	), nil
}
