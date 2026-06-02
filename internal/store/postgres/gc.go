package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GCRequest is the input for orphan document garbage collection.
type GCRequest struct {
	DryRun     bool // if true, only report candidates without deleting
	GraceHours int  // grace period in hours (default 1)
	Limit      int  // max candidates to show in dry-run (default 10)
}

// GCCandidate is a preview of an orphan document that would be deleted.
type GCCandidate struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	LegacyPath string `json:"legacy_path,omitempty"`
}

// GCResult is the output of garbage collection.
type GCResult struct {
	Count      int           `json:"count"`      // total orphan count (dry-run) or deleted count (force)
	Candidates []GCCandidate `json:"candidates"` // sample candidates (dry-run only)
}

// GC performs orphan document garbage collection.
// An orphan document is one that:
//   - has no references in gxfs_repo_paths
//   - has no references in gxfs_doc_namespace_paths
//   - has no references in gxfs_docset_docs
//   - was last updated more than GraceHours ago (to protect fresh creates in progress)
//
// In dry-run mode, it reports the count and sample candidates without deleting.
// In force mode, it deletes the orphan documents and reports the count.
func GC(ctx context.Context, pool *pgxpool.Pool, schema string, req GCRequest) (*GCResult, error) {
	if req.GraceHours <= 0 {
		req.GraceHours = 1
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}

	docsTable, err := quoteTable(schema, "gxfs_docs")
	if err != nil {
		return nil, err
	}

	orphanCondition, err := gcOrphanCondition(schema, req.GraceHours)
	if err != nil {
		return nil, err
	}

	if req.DryRun {
		return gcDryRun(ctx, pool, docsTable, orphanCondition, req.Limit)
	}
	return gcForce(ctx, pool, docsTable, orphanCondition)
}

func gcOrphanCondition(schema string, graceHours int) (string, error) {
	pathsTable, err := quoteTable(schema, "gxfs_repo_paths")
	if err != nil {
		return "", err
	}
	namespacePathsTable, err := quoteTable(schema, "gxfs_doc_namespace_paths")
	if err != nil {
		return "", err
	}
	docsetDocsTable, err := quoteTable(schema, "gxfs_docset_docs")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`
		updated_at < NOW() - INTERVAL '%d hours'
		AND NOT EXISTS (SELECT 1 FROM %s p WHERE p.doc_id = d.id)
		AND NOT EXISTS (SELECT 1 FROM %s np WHERE np.doc_id = d.id)
		AND NOT EXISTS (SELECT 1 FROM %s c WHERE c.doc_id = d.id)`,
		graceHours, pathsTable, namespacePathsTable, docsetDocsTable), nil
}

func gcDryRun(ctx context.Context, pool *pgxpool.Pool, docsTable, orphanCondition string, limit int) (*GCResult, error) {
	// Get total count
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM %s d WHERE %s", docsTable, orphanCondition)
	var count int
	if err := pool.QueryRow(ctx, countSQL).Scan(&count); err != nil {
		return nil, fmt.Errorf("gc count: %w", err)
	}

	// Get sample candidates
	candidatesSQL := fmt.Sprintf(`
		SELECT id, title, COALESCE(legacy_path, '')
		FROM %s d
		WHERE %s
		ORDER BY updated_at DESC
		LIMIT %d`,
		docsTable, orphanCondition, limit)

	rows, err := pool.Query(ctx, candidatesSQL)
	if err != nil {
		return nil, fmt.Errorf("gc candidates: %w", err)
	}
	defer rows.Close()

	var candidates []GCCandidate
	for rows.Next() {
		var c GCCandidate
		if err := rows.Scan(&c.ID, &c.Title, &c.LegacyPath); err != nil {
			return nil, fmt.Errorf("gc scan candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if candidates == nil {
		candidates = []GCCandidate{}
	}

	return &GCResult{Count: count, Candidates: candidates}, nil
}

func gcForce(ctx context.Context, pool *pgxpool.Pool, docsTable, orphanCondition string) (*GCResult, error) {
	deleteSQL := fmt.Sprintf("DELETE FROM %s d WHERE %s", docsTable, orphanCondition)

	result, err := pool.Exec(ctx, deleteSQL)
	if err != nil {
		return nil, fmt.Errorf("gc delete: %w", err)
	}

	return &GCResult{Count: int(result.RowsAffected())}, nil
}

// GCWithPool creates a temporary connection and runs GC.
// This is a convenience function for CLI use where we don't have an existing pool.
func GCWithPool(ctx context.Context, dsn, schema string, req GCRequest) (*GCResult, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("gc connect: %w", err)
	}
	defer pool.Close()

	return GC(ctx, pool, schema, req)
}
