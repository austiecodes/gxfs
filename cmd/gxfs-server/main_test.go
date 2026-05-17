package main

import (
	"context"
	"strings"
	"testing"

	"gxfs/internal/config"
)

func TestSplitAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		host string
		port int
	}{
		{name: "port only", addr: ":7635", host: "0.0.0.0", port: 7635},
		{name: "host and port", addr: "127.0.0.1:9000", host: "127.0.0.1", port: 9000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := splitAddr(tt.addr)
			if err != nil {
				t.Fatalf("splitAddr() error = %v", err)
			}
			if host != tt.host || port != tt.port {
				t.Fatalf("splitAddr() = %s %d, want %s %d", host, port, tt.host, tt.port)
			}
		})
	}
}

func TestPostgresConfigFromRepoDefaultsFileTable(t *testing.T) {
	cfg := postgresConfigFromRepo(config.RepoConfig{
		Name: "gxfs",
		Backend: config.BackendConfig{
			Type: "postgres",
			Postgres: config.PostgresConfig{
				DSN:    "postgres://localhost/gxfs",
				Schema: "public",
			},
		},
	})

	if cfg.DSN != "postgres://localhost/gxfs" || cfg.Schema != "public" {
		t.Fatalf("postgres config = %+v, want dsn/schema", cfg)
	}
	if cfg.NodesTable != "vfs_nodes" || cfg.ContentTable != "vfs_content" ||
		cfg.RepoNodesTable != "vfs_repo_nodes" ||
		cfg.Files.PathColumn != "path" || cfg.Files.KindColumn != "kind" ||
		cfg.Files.SizeColumn != "size" || cfg.Files.MTimeColumn != "updated_at" {
		t.Fatalf("config = %+v, want default table mapping", cfg)
	}
}

func TestAdapterFromServerConfigRejectsDuplicateRepos(t *testing.T) {
	_, err := adapterFromServerConfig(context.Background(), config.ServerConfig{
		Repos: []config.RepoConfig{
			{Name: "gxfs", Backend: config.BackendConfig{Type: "postgres"}},
			{Name: "gxfs", Backend: config.BackendConfig{Type: "postgres"}},
		},
	})
	if err == nil {
		t.Fatal("adapterFromServerConfig() error = nil, want duplicate repo error")
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		expected string
	}{
		{
			name:     "postgres with credentials",
			dsn:      "postgres://user:password@localhost:5432/mydb?sslmode=disable",
			expected: "postgres://localhost:5432/mydb?sslmode=disable",
		},
		{
			name:     "postgres without credentials",
			dsn:      "postgres://localhost:5432/mydb",
			expected: "postgres://localhost:5432/mydb",
		},
		{
			name:     "invalid DSN",
			dsn:      "not a valid dsn",
			expected: "<redacted>",
		},
		{
			name:     "postgres with user only",
			dsn:      "postgres://user@localhost:5432/mydb",
			expected: "postgres://localhost:5432/mydb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactDSN(tt.dsn)
			if got != tt.expected {
				t.Errorf("redactDSN(%q) = %q, want %q", tt.dsn, got, tt.expected)
			}
		})
	}
}

func TestCollectGCTargets(t *testing.T) {
	tests := []struct {
		name           string
		repos          []config.RepoConfig
		expectedCount  int
		expectedLabels []string // in sorted order
	}{
		{
			name: "single doc_postgres repo",
			repos: []config.RepoConfig{
				{
					Name: "repo1",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://user:pass@host1:5432/db1",
							Schema: "public",
						},
					},
				},
			},
			expectedCount: 1,
			expectedLabels: []string{
				"postgres://host1:5432/db1/public",
			},
		},
		{
			name: "multiple doc_postgres repos same target (dedupe)",
			repos: []config.RepoConfig{
				{
					Name: "repo1",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://user:pass@host1:5432/db1",
							Schema: "public",
						},
					},
				},
				{
					Name: "repo2",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://user:pass@host1:5432/db1", // Same DSN
							Schema: "public",                               // Same schema
						},
					},
				},
			},
			expectedCount: 1,
			expectedLabels: []string{
				"postgres://host1:5432/db1/public",
			},
		},
		{
			name: "multiple doc_postgres repos different schemas",
			repos: []config.RepoConfig{
				{
					Name: "repo1",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://user:pass@host1:5432/db1",
							Schema: "public",
						},
					},
				},
				{
					Name: "repo2",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://user:pass@host1:5432/db1",
							Schema: "tenant1",
						},
					},
				},
			},
			expectedCount: 2,
			expectedLabels: []string{
				"postgres://host1:5432/db1/public",
				"postgres://host1:5432/db1/tenant1",
			},
		},
		{
			name: "skip non-doc_postgres repos",
			repos: []config.RepoConfig{
				{
					Name: "repo1",
					Backend: config.BackendConfig{
						Type: "postgres", // Legacy postgres, not doc_postgres
						Postgres: config.PostgresConfig{
							DSN:    "postgres://user:pass@host1:5432/db1",
							Schema: "public",
						},
					},
				},
				{
					Name: "repo2",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://user:pass@host2:5432/db2",
							Schema: "public",
						},
					},
				},
			},
			expectedCount: 1,
			expectedLabels: []string{
				"postgres://host2:5432/db2/public",
			},
		},
		{
			name: "skip repos with empty DSN",
			repos: []config.RepoConfig{
				{
					Name: "repo1",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "", // Empty DSN
							Schema: "public",
						},
					},
				},
				{
					Name: "repo2",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://user:pass@host1:5432/db1",
							Schema: "public",
						},
					},
				},
			},
			expectedCount: 1,
			expectedLabels: []string{
				"postgres://host1:5432/db1/public",
			},
		},
		{
			name:          "no doc_postgres repos",
			repos:         []config.RepoConfig{},
			expectedCount: 0,
		},
		{
			name: "sorted output",
			repos: []config.RepoConfig{
				{
					Name: "repo_b",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://host_b:5432/db",
							Schema: "schema_b",
						},
					},
				},
				{
					Name: "repo_a",
					Backend: config.BackendConfig{
						Type: "doc_postgres",
						Postgres: config.PostgresConfig{
							DSN:    "postgres://host_a:5432/db",
							Schema: "schema_a",
						},
					},
				},
			},
			expectedCount: 2,
			expectedLabels: []string{
				"postgres://host_a:5432/db/schema_a",
				"postgres://host_b:5432/db/schema_b",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.ServerConfig{Repos: tt.repos}
			targets := collectGCTargets(cfg)

			if len(targets) != tt.expectedCount {
				t.Errorf("collectGCTargets() returned %d targets, want %d", len(targets), tt.expectedCount)
			}

			if tt.expectedLabels != nil {
				var gotLabels []string
				for _, target := range targets {
					gotLabels = append(gotLabels, target.label())
				}
				for i, got := range gotLabels {
					if i >= len(tt.expectedLabels) {
						t.Errorf("unexpected extra target: %s", got)
						break
					}
					if got != tt.expectedLabels[i] {
						t.Errorf("target[%d] = %q, want %q", i, got, tt.expectedLabels[i])
					}
				}
			}
		})
	}
}

func TestStorageTargetLabel(t *testing.T) {
	target := storageTarget{
		dsn:    "postgres://secret:password@prod-db:5432/gxfs",
		schema: "tenant1",
	}

	label := target.label()

	// Should not contain credentials
	if strings.Contains(label, "secret") || strings.Contains(label, "password") {
		t.Errorf("label %q contains credentials", label)
	}

	// Should contain schema
	if !strings.Contains(label, "tenant1") {
		t.Errorf("label %q missing schema", label)
	}
}
