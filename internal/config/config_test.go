package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadCLIConfig(t *testing.T) {
	path := writeConfig(t, "settings.toml", `
repo = "gxfs"

[server]
addr = "http://127.0.0.1:7635"

[mount]
include = ["/go", "/docs"]
exclude = ["vendor/**", "generated/**"]
`)

	cfg, err := LoadCLI(path)
	if err != nil {
		t.Fatalf("LoadCLI() error = %v", err)
	}
	if cfg.Repo != "gxfs" || cfg.Server.Addr != "http://127.0.0.1:7635" {
		t.Fatalf("LoadCLI() = %+v, want repo and server", cfg)
	}
	if len(cfg.Mount.Include) != 2 || cfg.Mount.Exclude[1] != "generated/**" {
		t.Fatalf("LoadCLI().Mount = %+v, want include/exclude", cfg.Mount)
	}
}

func TestLoadCLIRejectsBackendConfig(t *testing.T) {
	path := writeConfig(t, "settings.toml", `
repo = "gxfs"

[server]
addr = "http://127.0.0.1:7635"

[backend]
type = "postgres"
`)

	_, err := LoadCLI(path)
	if err == nil {
		t.Fatal("LoadCLI() error = nil, want backend rejection")
	}
}

func TestLoadServerConfigExpandsEnv(t *testing.T) {
	t.Setenv("GXFS_POSTGRES_DSN", "postgres://user:pass@localhost/gxfs")

	path := writeConfig(t, "server.toml", `
addr = ":7635"

[[repos]]
name = "gxfs"

[repos.backend]
type = "postgres"

[repos.backend.postgres]
dsn = "${GXFS_POSTGRES_DSN}"
schema = "public"
`)

	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	if cfg.Addr != ":7635" || len(cfg.Repos) != 1 {
		t.Fatalf("LoadServer() = %+v, want addr and one repo", cfg)
	}
	repo := cfg.Repos[0]
	if repo.Name != "gxfs" || repo.Backend.Type != "postgres" {
		t.Fatalf("repo = %+v, want postgres gxfs repo", repo)
	}
	if repo.Backend.Postgres.DSN != "postgres://user:pass@localhost/gxfs" {
		t.Fatalf("dsn = %q, want expanded env", repo.Backend.Postgres.DSN)
	}
}

func TestLoadServerConfigParsesPostgresFileTable(t *testing.T) {
	path := writeConfig(t, "server.toml", `
addr = ":7635"

[[repos]]
name = "gxfs"

[repos.backend]
type = "postgres"

[repos.backend.postgres]
dsn = "postgres://localhost/gxfs"
schema = "public"
nodes_table = "my_nodes"
content_table = "my_content"

[repos.backend.postgres.files]
path_column = "file_path"
kind_column = "node_type"
size_column = "byte_size"
mtime_column = "changed_at"
`)

	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	pg := cfg.Repos[0].Backend.Postgres
	if pg.NodesTable != "my_nodes" || pg.ContentTable != "my_content" {
		t.Fatalf("postgres tables = %q/%q, want my_nodes/my_content", pg.NodesTable, pg.ContentTable)
	}
	files := pg.Files
	if files.PathColumn != "file_path" || files.KindColumn != "node_type" ||
		files.SizeColumn != "byte_size" || files.MTimeColumn != "changed_at" {
		t.Fatalf("postgres files = %+v, want custom mapping", files)
	}
}
