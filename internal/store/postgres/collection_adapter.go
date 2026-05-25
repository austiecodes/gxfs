package postgres

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/austiecodes/gxfs/internal/store"
)

// CollectionAdapter implements store.CollectionManager over gxfs_collections and gxfs_collection_docs.
type CollectionAdapter struct {
	pool   *pgxpool.Pool
	schema string
}

var _ store.CollectionManager = (*CollectionAdapter)(nil)

// collectionNameRegex validates collection names: lowercase alphanumeric, dash, underscore only.
var collectionNameRegex = regexp.MustCompile(`^[a-z0-9_-]+$`)

// NewCollectionAdapter creates a CollectionAdapter.
func NewCollectionAdapter(pool *pgxpool.Pool, schema string) *CollectionAdapter {
	return &CollectionAdapter{pool: pool, schema: schema}
}

func (c *CollectionAdapter) collectionsTable() string {
	tbl, _ := quoteTable(c.schema, "gxfs_collections")
	return tbl
}

func (c *CollectionAdapter) collectionDocsTable() string {
	tbl, _ := quoteTable(c.schema, "gxfs_collection_docs")
	return tbl
}

func (c *CollectionAdapter) docsTable() string {
	tbl, _ := quoteTable(c.schema, "gxfs_docs")
	return tbl
}

func (c *CollectionAdapter) repoPathsTable() string {
	tbl, _ := quoteTable(c.schema, "gxfs_repo_paths")
	return tbl
}

// CreateCollection creates a new collection.
func (c *CollectionAdapter) CreateCollection(ctx context.Context, req store.CreateCollectionRequest) (*store.CreateCollectionResponse, error) {
	// Validate name
	if !collectionNameRegex.MatchString(req.Name) {
		return nil, store.ErrInvalidName
	}

	now := time.Now().UTC()
	var id string
	err := c.pool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO %s (name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, c.collectionsTable()), req.Name, req.Description, now, now).Scan(&id)
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, store.ErrNameExists
		}
		return nil, fmt.Errorf("create collection: %w", err)
	}

	return &store.CreateCollectionResponse{
		Collection: store.Collection{
			ID:          id,
			Name:        req.Name,
			Description: req.Description,
			CreatedAt:   now.Format(time.RFC3339),
			UpdatedAt:   now.Format(time.RFC3339),
		},
	}, nil
}

// ListCollections lists all collections.
func (c *CollectionAdapter) ListCollections(ctx context.Context) (*store.ListCollectionsResponse, error) {
	rows, err := c.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, name, description, created_at, updated_at
		FROM %s
		ORDER BY name
	`, c.collectionsTable()))
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()

	var collections []store.Collection
	for rows.Next() {
		var col store.Collection
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&col.ID, &col.Name, &col.Description, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		col.CreatedAt = createdAt.Format(time.RFC3339)
		col.UpdatedAt = updatedAt.Format(time.RFC3339)
		collections = append(collections, col)
	}

	if collections == nil {
		collections = []store.Collection{}
	}

	return &store.ListCollectionsResponse{Collections: collections}, nil
}

// GetCollection gets a collection by name with its members.
func (c *CollectionAdapter) GetCollection(ctx context.Context, name string) (*store.GetCollectionResponse, error) {
	var col store.Collection
	var createdAt, updatedAt time.Time
	err := c.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT id, name, description, created_at, updated_at
		FROM %s
		WHERE name = $1
	`, c.collectionsTable()), name).Scan(&col.ID, &col.Name, &col.Description, &createdAt, &updatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, store.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("get collection: %w", err)
	}
	col.CreatedAt = createdAt.Format(time.RFC3339)
	col.UpdatedAt = updatedAt.Format(time.RFC3339)

	// Get members
	rows, err := c.pool.Query(ctx, fmt.Sprintf(`
		SELECT cd.path, cd.doc_id
		FROM %s cd
		WHERE cd.collection_id = $1
		ORDER BY cd.path
	`, c.collectionDocsTable()), col.ID)
	if err != nil {
		return nil, fmt.Errorf("get collection members: %w", err)
	}
	defer rows.Close()

	var members []store.CollectionMember
	for rows.Next() {
		var m store.CollectionMember
		if err := rows.Scan(&m.Path, &m.DocID); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, m)
	}

	if members == nil {
		members = []store.CollectionMember{}
	}

	return &store.GetCollectionResponse{Collection: col, Members: members}, nil
}

// DeleteCollection deletes a collection and its members in a single transaction.
func (c *CollectionAdapter) DeleteCollection(ctx context.Context, name string) error {
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

	// Get collection ID first
	var collectionID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT id FROM %s WHERE name = $1
	`, c.collectionsTable()), name).Scan(&collectionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return store.ErrCollectionNotFound
		}
		return fmt.Errorf("get collection: %w", err)
	}

	// Delete members first (no ON DELETE CASCADE in schema)
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s WHERE collection_id = $1
	`, c.collectionDocsTable()), collectionID)
	if err != nil {
		return fmt.Errorf("delete collection members: %w", err)
	}

	// Delete collection
	result, err := tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s WHERE id = $1
	`, c.collectionsTable()), collectionID)
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	if result.RowsAffected() == 0 {
		return store.ErrCollectionNotFound
	}

	// Commit transaction
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// AddMember adds a document to a collection.
// source_ref must be in format repo://repo-name/path (repo-name is URL-encoded if it contains /)
func (c *CollectionAdapter) AddMember(ctx context.Context, req store.AddMemberRequest) (*store.AddMemberResponse, error) {
	// Parse source_ref: repo://repo-name/path
	repoName, docPath, err := parseRepoRef(req.SourceRef)
	if err != nil {
		return nil, fmt.Errorf("invalid source_ref: %w", err)
	}

	// Get collection ID
	var collectionID string
	err = c.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT id FROM %s WHERE name = $1
	`, c.collectionsTable()), req.Name).Scan(&collectionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, store.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("get collection: %w", err)
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
		INSERT INTO %s (collection_id, doc_id, path)
		VALUES ($1, $2, $3)
	`, c.collectionDocsTable()), collectionID, docID, req.Path)
	if err != nil {
		if isDuplicateKeyError(err) {
			// Check which constraint was violated
			if isPathConflict(err) {
				return nil, store.ErrMemberExists
			}
			return nil, store.ErrDocAlreadyInCollection
		}
		return nil, fmt.Errorf("add member: %w", err)
	}

	// Update collection updated_at
	_, _ = c.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET updated_at = NOW() WHERE id = $1
	`, c.collectionsTable()), collectionID)

	return &store.AddMemberResponse{
		Member: store.CollectionMember{
			Path:  req.Path,
			DocID: docID,
		},
	}, nil
}

// RemoveMember removes a document from a collection.
func (c *CollectionAdapter) RemoveMember(ctx context.Context, req store.RemoveMemberRequest) error {
	result, err := c.pool.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s
		WHERE collection_id = (SELECT id FROM %s WHERE name = $1)
		AND path = $2
	`, c.collectionDocsTable(), c.collectionsTable()), req.Name, req.Path)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if result.RowsAffected() == 0 {
		return store.ErrNotFound
	}

	// Update collection updated_at
	_, _ = c.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET updated_at = NOW() WHERE name = $1
	`, c.collectionsTable()), req.Name)

	return nil
}

// GetMemberContent reads a document's content via collection membership.
func (c *CollectionAdapter) GetMemberContent(ctx context.Context, req store.GetMemberContentRequest) (*store.GetMemberContentResponse, error) {
	var content, hash string
	err := c.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT d.content, d.content_hash
		FROM %s d
		JOIN %s cd ON d.id = cd.doc_id
		JOIN %s c ON cd.collection_id = c.id
		WHERE c.name = $1 AND cd.path = $2
	`, c.docsTable(), c.collectionDocsTable(), c.collectionsTable()), req.Name, req.Path).Scan(&content, &hash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("get member content: %w", err)
	}

	return &store.GetMemberContentResponse{
		Path:    req.Path,
		Content: content,
		Hash:    hash,
	}, nil
}

// parseRepoRef parses repo://repo-name/path into (repoName, path).
// The repo-name segment is URL-decoded to handle repo names containing /.
// Only repo:// refs are accepted; collection:// refs are rejected with an error.
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
	return err != nil && containsErrorMsg(err, "gxfs_collection_docs_pkey")
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
