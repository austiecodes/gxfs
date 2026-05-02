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
	path := writeConfig(t, "gxfs.toml", `
project = "gxfs"

[server]
addr = "http://127.0.0.1:7635"

[mount]
include = ["/go", "/docs"]
exclude = ["vendor/**", "java-reference/**"]
`)

	cfg, err := LoadCLI(path)
	if err != nil {
		t.Fatalf("LoadCLI() error = %v", err)
	}
	if cfg.Project != "gxfs" || cfg.Server.Addr != "http://127.0.0.1:7635" {
		t.Fatalf("LoadCLI() = %+v, want project and server", cfg)
	}
	if len(cfg.Mount.Include) != 2 || cfg.Mount.Exclude[1] != "java-reference/**" {
		t.Fatalf("LoadCLI().Mount = %+v, want include/exclude", cfg.Mount)
	}
}

func TestLoadCLIRejectsBackendConfig(t *testing.T) {
	path := writeConfig(t, "gxfs.toml", `
project = "gxfs"

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

[repos.backend.postgres.files]
table = "knowledge_files"
path_column = "file_path"
content_column = "body"
size_column = "byte_size"
mtime_column = "changed_at"
`)

	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	files := cfg.Repos[0].Backend.Postgres.Files
	if files.Table != "knowledge_files" || files.PathColumn != "file_path" ||
		files.ContentColumn != "body" || files.SizeColumn != "byte_size" ||
		files.MTimeColumn != "changed_at" {
		t.Fatalf("postgres files = %+v, want custom mapping", files)
	}
}
