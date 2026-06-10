package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/austiecodes/rolio/internal/store"
)

func TestRegistrySQLBuilders(t *testing.T) {
	cfg := Config{Schema: "catalog"}

	tests := []struct {
		name string
		fn   func(Config) (string, error)
		want string
	}{
		{
			name: "ListReposSQL",
			fn:   ListReposSQL,
			want: `select name, writable, created_at, updated_at from "catalog"."rolio_repos" order by name`,
		},
		{
			name: "RegisterRepoSQL",
			fn:   RegisterRepoSQL,
			want: `insert into "catalog"."rolio_repos"(name, writable) values($1, $2) returning name, writable, created_at, updated_at`,
		},
		{
			name: "ListDocNamespacesSQL",
			fn:   ListDocNamespacesSQL,
			want: `select name, description, writable, created_at, updated_at from "catalog"."rolio_doc_namespaces" order by name`,
		},
		{
			name: "ListDocsetsSQL",
			fn:   ListDocsetsSQL,
			want: `select id::text, name, description, created_at, updated_at from "catalog"."rolio_docsets" order by name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, err := tt.fn(cfg)
			if err != nil {
				t.Fatalf("%s() error = %v", tt.name, err)
			}
			if sql != tt.want {
				t.Fatalf("%s() = %q, want %q", tt.name, sql, tt.want)
			}
		})
	}
}

func TestRegistrySQLRejectsUnsafeSchema(t *testing.T) {
	cfg := Config{Schema: "catalog; drop schema public"}
	for _, tt := range []struct {
		name string
		fn   func(Config) (string, error)
	}{
		{"ListReposSQL", ListReposSQL},
		{"RegisterRepoSQL", RegisterRepoSQL},
		{"ListDocNamespacesSQL", ListDocNamespacesSQL},
		{"ListDocsetsSQL", ListDocsetsSQL},
	} {
		if _, err := tt.fn(cfg); err == nil {
			t.Fatalf("%s() error = nil, want unsafe schema rejection", tt.name)
		}
	}
}

func TestRepoRegistryMigrationBackfillsExistingRepoPaths(t *testing.T) {
	statements, err := SchemaSQL(Config{
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
	})
	if err != nil {
		t.Fatalf("SchemaSQL() error = %v", err)
	}

	var repoRegistryStmt string
	for _, statement := range statements {
		if strings.Contains(statement, `"rolio_repos"`) {
			repoRegistryStmt = statement
			break
		}
	}
	if repoRegistryStmt == "" {
		t.Fatal("SchemaSQL() missing rolio_repos migration")
	}
	for _, want := range []string{
		`create table if not exists "public"."rolio_repos"`,
		`name text primary key`,
		`writable bool not null default false`,
		`select distinct repo`,
		`from "public"."rolio_repo_paths"`,
		`on conflict (name) do nothing`,
	} {
		if !strings.Contains(repoRegistryStmt, want) {
			t.Fatalf("repo registry migration missing %q:\n%s", want, repoRegistryStmt)
		}
	}
}

func TestSchemaSQLUsesDefaultsForInfraOnlyConfig(t *testing.T) {
	statements, err := SchemaSQL(Config{Schema: "public"})
	if err != nil {
		t.Fatalf("SchemaSQL() error = %v", err)
	}
	joined := strings.Join(statements, "\n")
	for _, want := range []string{
		`"public"."vfs_nodes"`,
		`"public"."vfs_content"`,
		`"public"."vfs_repo_nodes"`,
		`"path" text primary key`,
		`"kind" text not null default 'file'`,
		`"size" bigint not null default 0`,
		`"updated_at" timestamptz not null default now()`,
		`"public"."rolio_repos"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("SchemaSQL(Config{Schema: public}) missing default %q", want)
		}
	}
}

func TestIsRepoDuplicateError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "pg error primary key",
			err:  &pgconn.PgError{Code: postgresDuplicateKeySQLState, ConstraintName: "rolio_repos_pkey"},
			want: true,
		},
		{
			name: "pg error table name",
			err:  &pgconn.PgError{Code: postgresDuplicateKeySQLState, TableName: "rolio_repos"},
			want: true,
		},
		{
			name: "wrapped pg error",
			err:  fmt.Errorf("insert repo: %w", &pgconn.PgError{Code: postgresDuplicateKeySQLState, ConstraintName: "rolio_repos_pkey"}),
			want: true,
		},
		{
			name: "string fallback",
			err:  errors.New(`ERROR: duplicate key value violates unique constraint "rolio_repos_pkey"`),
			want: true,
		},
		{
			name: "other unique constraint",
			err:  &pgconn.PgError{Code: postgresDuplicateKeySQLState, ConstraintName: "rolio_docsets_name_key"},
			want: false,
		},
		{
			name: "generic duplicate text",
			err:  errors.New("duplicate key"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRepoDuplicateError(tt.err); got != tt.want {
				t.Fatalf("isRepoDuplicateError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRepoExistsErrorIsSpecific(t *testing.T) {
	if errors.Is(store.ErrDocsetNameExists, store.ErrRepoExists) {
		t.Fatal("ErrRepoExists should be distinct from docset ErrDocsetNameExists")
	}
	if !errors.Is(store.ErrRepoExists, store.ErrRepoExists) {
		t.Fatal("ErrRepoExists should be an errors.Is sentinel")
	}
}
