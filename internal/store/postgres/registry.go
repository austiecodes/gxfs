package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/austiecodes/gxfs/internal/store"
)

const (
	repoRegistryTable            = "gxfs_repos"
	docNamespaceRegistryTable    = "gxfs_doc_namespaces"
	docsetRegistrySourceTable    = "gxfs_docsets"
	postgresDuplicateKeySQLState = "23505"
)

// Registry exposes the DB-backed source namespace catalog.
type Registry struct {
	pool *pgxpool.Pool
	cfg  Config
}

var _ store.RepoRegistry = (*Registry)(nil)
var _ store.NamespaceCatalog = (*Registry)(nil)
var _ store.MountSourceLister = (*Registry)(nil)

// NewRegistry creates a registry backed by an existing Postgres pool.
func NewRegistry(pool *pgxpool.Pool, cfg Config) *Registry {
	return &Registry{pool: pool, cfg: cfg}
}

// ConnectRegistry connects to Postgres and ensures the registry schema exists.
func ConnectRegistry(ctx context.Context, cfg Config) (*Registry, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres registry: %w", err)
	}
	registry := NewRegistry(pool, cfg)
	if err := registry.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate postgres registry schema: %w", err)
	}
	return registry, nil
}

func (r *Registry) ensureSchema(ctx context.Context) error {
	statements, err := SchemaSQL(r.cfg)
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if _, err := r.pool.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// ListRepos returns all repository namespaces registered in Postgres.
func (r *Registry) ListRepos(ctx context.Context) ([]store.RepoInfo, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("postgres registry pool is nil")
	}
	query, err := ListReposSQL(r.cfg)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()

	var repos []store.RepoInfo
	for rows.Next() {
		var repo store.RepoInfo
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&repo.Name, &repo.Writable, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		repo.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		repo.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
		repos = append(repos, repo)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read repos: %w", err)
	}
	if repos == nil {
		repos = []store.RepoInfo{}
	}
	return repos, nil
}

// RegisterRepo inserts a repository namespace into the durable catalog.
func (r *Registry) RegisterRepo(ctx context.Context, req store.RegisterRepoRequest) (*store.RegisterRepoResponse, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("postgres registry pool is nil")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, store.ErrInvalidParam
	}
	query, err := RegisterRepoSQL(r.cfg)
	if err != nil {
		return nil, err
	}
	var repo store.RepoInfo
	var createdAt, updatedAt time.Time
	err = r.pool.QueryRow(ctx, query, name, req.Writable).Scan(&repo.Name, &repo.Writable, &createdAt, &updatedAt)
	if err != nil {
		if isRepoDuplicateError(err) {
			return nil, store.ErrRepoExists
		}
		return nil, fmt.Errorf("register repo: %w", err)
	}
	repo.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	repo.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return &store.RegisterRepoResponse{Repo: repo}, nil
}

// ListDocNamespaces returns all docs:// namespaces registered in Postgres.
func (r *Registry) ListDocNamespaces(ctx context.Context) ([]store.DocNamespace, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("postgres registry pool is nil")
	}
	query, err := ListDocNamespacesSQL(r.cfg)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list doc namespaces: %w", err)
	}
	defer rows.Close()

	var namespaces []store.DocNamespace
	for rows.Next() {
		var namespace store.DocNamespace
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&namespace.Name, &namespace.Description, &namespace.Writable, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan doc namespace: %w", err)
		}
		namespace.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		namespace.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
		namespaces = append(namespaces, namespace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read doc namespaces: %w", err)
	}
	if namespaces == nil {
		namespaces = []store.DocNamespace{}
	}
	return namespaces, nil
}

// ListDocsets returns curated docset:// namespaces from gxfs_docsets.
func (r *Registry) ListDocsets(ctx context.Context) ([]store.Docset, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("postgres registry pool is nil")
	}
	query, err := ListDocsetsSQL(r.cfg)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, query)
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
		docset.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		docset.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
		docsets = append(docsets, docset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read docsets: %w", err)
	}
	if docsets == nil {
		docsets = []store.Docset{}
	}
	return docsets, nil
}

// MountSources returns repo://, docs://, and docset:// sources from the DB catalog.
func (r *Registry) MountSources(ctx context.Context) ([]store.MountSource, error) {
	repos, err := r.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	namespaces, err := r.ListDocNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	docsets, err := r.ListDocsets(ctx)
	if err != nil {
		return nil, err
	}

	sources := make([]store.MountSource, 0, len(repos)+len(namespaces)+len(docsets))
	for _, repo := range repos {
		ref := store.SourceRef{Kind: store.SourceKindRepo, Name: repo.Name}.String()
		sources = append(sources, store.MountSource{
			Ref:         ref,
			Kind:        store.SourceKindRepo,
			Name:        repo.Name,
			Writable:    repo.Writable,
			Description: "repository namespace",
		})
	}
	for _, namespace := range namespaces {
		ref := store.SourceRef{Kind: store.SourceKindDocs, Name: namespace.Name}.String()
		description := namespace.Description
		if description == "" {
			description = "shared docs namespace"
		}
		sources = append(sources, store.MountSource{
			Ref:         ref,
			Kind:        store.SourceKindDocs,
			Name:        namespace.Name,
			Writable:    namespace.Writable,
			Description: description,
		})
	}
	for _, docset := range docsets {
		ref := store.SourceRef{Kind: store.SourceKindDocset, Name: docset.Name}.String()
		description := docset.Description
		if description == "" {
			description = "curated docset"
		}
		sources = append(sources, store.MountSource{
			Ref:         ref,
			Kind:        store.SourceKindDocset,
			Name:        docset.Name,
			Description: description,
		})
	}
	sort.SliceStable(sources, func(i, j int) bool {
		return sources[i].Ref < sources[j].Ref
	})
	return sources, nil
}

func ListReposSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, repoRegistryTable)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("select name, writable, created_at, updated_at from %s order by name", table), nil
}

func RegisterRepoSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, repoRegistryTable)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"insert into %s(name, writable) values($1, $2) returning name, writable, created_at, updated_at",
		table,
	), nil
}

func ListDocNamespacesSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, docNamespaceRegistryTable)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("select name, description, writable, created_at, updated_at from %s order by name", table), nil
}

func ListDocsetsSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, docsetRegistrySourceTable)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("select id::text, name, description, created_at, updated_at from %s order by name", table), nil
}

func isRepoDuplicateError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == postgresDuplicateKeySQLState &&
			(pgErr.ConstraintName == "gxfs_repos_pkey" || pgErr.TableName == repoRegistryTable)
	}
	return isRepoDuplicateMessage(err)
}

func isRepoDuplicateMessage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return (strings.Contains(msg, "duplicate key") || strings.Contains(msg, "violates unique constraint")) &&
		(strings.Contains(msg, "gxfs_repos_pkey") || strings.Contains(msg, repoRegistryTable))
}
