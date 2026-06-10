package postgres

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/austiecodes/rolio/internal/store"
)

// DocsetAdapter implements store.DocsetManager over rolio_docsets and rolio_docset_docs.
type DocsetAdapter struct {
	pool   *pgxpool.Pool
	schema string
}

var _ store.DocsetManager = (*DocsetAdapter)(nil)

// docsetNameRegex validates docset names: lowercase alphanumeric, dash, underscore only.
var docsetNameRegex = regexp.MustCompile(`^[a-z0-9_-]+$`)

// NewDocsetAdapter creates a DocsetAdapter.
func NewDocsetAdapter(pool *pgxpool.Pool, schema string) *DocsetAdapter {
	return &DocsetAdapter{pool: pool, schema: schema}
}

func (c *DocsetAdapter) docsetsTable() string {
	tbl, _ := quoteTable(c.schema, "rolio_docsets")
	return tbl
}

func (c *DocsetAdapter) docsetDocsTable() string {
	tbl, _ := quoteTable(c.schema, "rolio_docset_docs")
	return tbl
}

func (c *DocsetAdapter) docsTable() string {
	tbl, _ := quoteTable(c.schema, "rolio_docs")
	return tbl
}

func (c *DocsetAdapter) repoPathsTable() string {
	tbl, _ := quoteTable(c.schema, "rolio_repo_paths")
	return tbl
}

// CreateDocset creates a new docset.
func (c *DocsetAdapter) CreateDocset(ctx context.Context, req store.CreateDocsetRequest) (*store.CreateDocsetResponse, error) {
	// Validate name
	if !docsetNameRegex.MatchString(req.Name) {
		return nil, store.ErrInvalidName
	}

	now := time.Now().UTC()
	var id string
	err := c.pool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO %s (name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, c.docsetsTable()), req.Name, req.Description, now, now).Scan(&id)
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, store.ErrDocsetNameExists
		}
		return nil, fmt.Errorf("create docset: %w", err)
	}

	return &store.CreateDocsetResponse{
		Docset: store.Docset{
			ID:          id,
			Name:        req.Name,
			Description: req.Description,
			CreatedAt:   now.Format(time.RFC3339),
			UpdatedAt:   now.Format(time.RFC3339),
		},
	}, nil
}

// ListDocsets lists all docsets.
func (c *DocsetAdapter) ListDocsets(ctx context.Context) (*store.ListDocsetsResponse, error) {
	rows, err := c.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, name, description, created_at, updated_at
		FROM %s
		ORDER BY name
	`, c.docsetsTable()))
	if err != nil {
		return nil, fmt.Errorf("list docsets: %w", err)
	}
	defer rows.Close()

	var docsets []store.Docset
	for rows.Next() {
		var docset store.Docset
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&docset.ID, &docset.Name, &docset.Description, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan docset: %w", err)
		}
		docset.CreatedAt = createdAt.Format(time.RFC3339)
		docset.UpdatedAt = updatedAt.Format(time.RFC3339)
		docsets = append(docsets, docset)
	}

	if docsets == nil {
		docsets = []store.Docset{}
	}

	return &store.ListDocsetsResponse{Docsets: docsets}, nil
}

// GetDocset gets a docset by name with its members.
func (c *DocsetAdapter) GetDocset(ctx context.Context, name string) (*store.GetDocsetResponse, error) {
	var docset store.Docset
	var createdAt, updatedAt time.Time
	err := c.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT id, name, description, created_at, updated_at
		FROM %s
		WHERE name = $1
	`, c.docsetsTable()), name).Scan(&docset.ID, &docset.Name, &docset.Description, &createdAt, &updatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, store.ErrDocsetNotFound
		}
		return nil, fmt.Errorf("get docset: %w", err)
	}
	docset.CreatedAt = createdAt.Format(time.RFC3339)
	docset.UpdatedAt = updatedAt.Format(time.RFC3339)

	// Get members
	rows, err := c.pool.Query(ctx, fmt.Sprintf(`
		SELECT cd.path, cd.doc_id
		FROM %s cd
		WHERE cd.docset_id = $1
		ORDER BY cd.path
	`, c.docsetDocsTable()), docset.ID)
	if err != nil {
		return nil, fmt.Errorf("get docset members: %w", err)
	}
	defer rows.Close()

	var members []store.DocsetMember
	for rows.Next() {
		var m store.DocsetMember
		if err := rows.Scan(&m.Path, &m.DocID); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, m)
	}

	if members == nil {
		members = []store.DocsetMember{}
	}

	return &store.GetDocsetResponse{Docset: docset, Members: members}, nil
}

// DeleteDocset deletes a docset and its members in a single transaction.
func (c *DocsetAdapter) DeleteDocset(ctx context.Context, name string) error {
	// Use a transaction to ensure atomicity
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	// Get docset ID first
	var docsetID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT id FROM %s WHERE name = $1
	`, c.docsetsTable()), name).Scan(&docsetID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return store.ErrDocsetNotFound
		}
		return fmt.Errorf("get docset: %w", err)
	}

	// Delete members first (no ON DELETE CASCADE in schema)
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s WHERE docset_id = $1
	`, c.docsetDocsTable()), docsetID)
	if err != nil {
		return fmt.Errorf("delete docset members: %w", err)
	}

	// Delete docset
	result, err := tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s WHERE id = $1
	`, c.docsetsTable()), docsetID)
	if err != nil {
		return fmt.Errorf("delete docset: %w", err)
	}
	if result.RowsAffected() == 0 {
		return store.ErrDocsetNotFound
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// AddDocsetMember adds a document to a docset.
// source_ref must be in format repo://repo-name/path (repo-name is URL-encoded if it contains /)
func (c *DocsetAdapter) AddDocsetMember(ctx context.Context, req store.AddDocsetMemberRequest) (*store.AddDocsetMemberResponse, error) {
	// Parse source_ref: repo://repo-name/path
	repoName, docPath, err := parseRepoRef(req.SourceRef)
	if err != nil {
		return nil, fmt.Errorf("invalid source_ref: %w", err)
	}

	// Get docset ID
	var docsetID string
	err = c.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT id FROM %s WHERE name = $1
	`, c.docsetsTable()), req.Name).Scan(&docsetID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, store.ErrDocsetNotFound
		}
		return nil, fmt.Errorf("get docset: %w", err)
	}

	// Find doc_id from repo_paths
	var docID string
	err = c.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT doc_id FROM %s WHERE repo = $1 AND path = $2
	`, c.repoPathsTable()), repoName, docPath).Scan(&docID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("find doc: %w", err)
	}

	// Insert member
	_, err = c.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (docset_id, doc_id, path)
		VALUES ($1, $2, $3)
	`, c.docsetDocsTable()), docsetID, docID, req.Path)
	if err != nil {
		if isDuplicateKeyError(err) {
			// Check which constraint was violated
			if isPathConflict(err) {
				return nil, store.ErrDocsetMemberExists
			}
			return nil, store.ErrDocAlreadyInDocset
		}
		return nil, fmt.Errorf("add member: %w", err)
	}

	// Update docset updated_at
	_, _ = c.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET updated_at = NOW() WHERE id = $1
	`, c.docsetsTable()), docsetID)

	return &store.AddDocsetMemberResponse{
		Member: store.DocsetMember{
			Path:  req.Path,
			DocID: docID,
		},
	}, nil
}

// RemoveDocsetMember removes a document from a docset.
func (c *DocsetAdapter) RemoveDocsetMember(ctx context.Context, req store.RemoveDocsetMemberRequest) error {
	result, err := c.pool.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s
		WHERE docset_id = (SELECT id FROM %s WHERE name = $1)
		AND path = $2
	`, c.docsetDocsTable(), c.docsetsTable()), req.Name, req.Path)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if result.RowsAffected() == 0 {
		return store.ErrNotFound
	}

	// Update docset updated_at
	_, _ = c.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET updated_at = NOW() WHERE name = $1
	`, c.docsetsTable()), req.Name)

	return nil
}

// GetDocsetMemberContent reads a document's content via docset membership.
func (c *DocsetAdapter) GetDocsetMemberContent(ctx context.Context, req store.GetDocsetMemberContentRequest) (*store.GetDocsetMemberContentResponse, error) {
	var content, hash string
	err := c.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT d.content, d.content_hash
		FROM %s d
		JOIN %s cd ON d.id = cd.doc_id
		JOIN %s c ON cd.docset_id = c.id
		WHERE c.name = $1 AND cd.path = $2
	`, c.docsTable(), c.docsetDocsTable(), c.docsetsTable()), req.Name, req.Path).Scan(&content, &hash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("get member content: %w", err)
	}

	return &store.GetDocsetMemberContentResponse{
		Path:    req.Path,
		Content: content,
		Hash:    hash,
	}, nil
}

// parseRepoRef parses repo://repo-name/path into (repoName, path).
// The repo-name segment is URL-decoded to handle repo names containing /.
// Only repo:// refs are accepted; docset:// refs are rejected with an error.
func parseRepoRef(ref string) (string, string, error) {
	if len(ref) < 7 || ref[:7] != "repo://" {
		return "", "", fmt.Errorf("source_ref must start with repo:// (non-repo refs not supported in v1)")
	}
	rest := ref[7:]
	// Find the first /
	slashIdx := -1
	for i, c := range rest {
		if c == '/' {
			slashIdx = i
			break
		}
	}
	if slashIdx <= 0 {
		return "", "", fmt.Errorf("invalid repo ref format")
	}
	encodedRepoName := rest[:slashIdx]
	path := rest[slashIdx:]
	if path == "" {
		return "", "", fmt.Errorf("path is required")
	}

	// URL-decode the repo name (handles repo names with / like "github/openai-go")
	repoName, err := url.PathUnescape(encodedRepoName)
	if err != nil {
		return "", "", fmt.Errorf("invalid repo name encoding: %w", err)
	}

	return repoName, path, nil
}

// isDuplicateKeyError checks if the error is a duplicate key violation.
func isDuplicateKeyError(err error) bool {
	return err != nil && (containsErrorMsg(err, "duplicate key") || containsErrorMsg(err, "violates unique constraint"))
}

// isPathConflict checks if the duplicate key error is for the path constraint.
func isPathConflict(err error) bool {
	return err != nil && containsErrorMsg(err, "rolio_docset_docs_pkey")
}

func containsErrorMsg(err error, substr string) bool {
	return len(err.Error()) >= len(substr) && (err.Error() == substr ||
		(len(err.Error()) > len(substr) && findSubstring(err.Error(), substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
