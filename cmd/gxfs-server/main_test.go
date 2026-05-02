package main

import (
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
	if cfg.Files.Table != "vfs_files" || cfg.Files.PathColumn != "path" ||
		cfg.Files.ContentColumn != "content" || cfg.Files.SizeColumn != "size" ||
		cfg.Files.MTimeColumn != "updated_at" {
		t.Fatalf("files config = %+v, want default file table mapping", cfg.Files)
	}
}
