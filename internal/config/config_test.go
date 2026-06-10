package config

import (
	"os"
	"path/filepath"
	"strings"
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
version = 1
repo = "rolio"

[server]
addr = "http://127.0.0.1:7635"

[auth]
mode = "bearer"
token_env = "ROLIO_TOKEN"

[mount]
include = ["/go", "/docs"]
exclude = ["vendor/**", "generated/**"]

[docs]
path = "internal-docs"

[cache]
metadata_ttl = "5m"
content_ttl = "24h"
materialize = "explicit"
`)

	cfg, err := LoadCLI(path)
	if err != nil {
		t.Fatalf("LoadCLI() error = %v", err)
	}
	if cfg.Version != 1 || cfg.Repo != "rolio" || cfg.Server.Addr != "http://127.0.0.1:7635" {
		t.Fatalf("LoadCLI() = %+v, want version, repo and server", cfg)
	}
	if len(cfg.Mount.Include) != 2 || cfg.Mount.Exclude[1] != "generated/**" {
		t.Fatalf("LoadCLI().Mount = %+v, want include/exclude", cfg.Mount)
	}
	if cfg.Auth.Mode != "bearer" || cfg.Auth.TokenEnv != "ROLIO_TOKEN" {
		t.Fatalf("LoadCLI().Auth = %+v, want bearer token env", cfg.Auth)
	}
	if cfg.Cache.MetadataTTL != "5m" || cfg.Cache.ContentTTL != "24h" || cfg.Cache.Materialize != "explicit" {
		t.Fatalf("LoadCLI().Cache = %+v, want cache settings", cfg.Cache)
	}
	if cfg.Docs.Path != "internal-docs" {
		t.Fatalf("LoadCLI().Docs.Path = %q, want internal-docs", cfg.Docs.Path)
	}
}

func TestLoadCLIConfigDefaultsDocsPath(t *testing.T) {
	path := writeConfig(t, "settings.toml", `
repo = "rolio"

[server]
addr = "http://127.0.0.1:7635"
`)

	cfg, err := LoadCLI(path)
	if err != nil {
		t.Fatalf("LoadCLI() error = %v", err)
	}
	if cfg.Version != 1 {
		t.Fatalf("LoadCLI().Version = %d, want 1", cfg.Version)
	}
	if cfg.Docs.Path != "docs" {
		t.Fatalf("LoadCLI().Docs.Path = %q, want docs", cfg.Docs.Path)
	}
	if cfg.Auth.Mode != "none" {
		t.Fatalf("LoadCLI().Auth.Mode = %q, want none", cfg.Auth.Mode)
	}
	if cfg.Cache.MetadataTTL != "5m" || cfg.Cache.ContentTTL != "24h" || cfg.Cache.Materialize != "explicit" {
		t.Fatalf("LoadCLI().Cache = %+v, want defaults", cfg.Cache)
	}
}

func TestLoadCLIConfigCleansLegacyDocsPath(t *testing.T) {
	path := writeConfig(t, "settings.toml", `
repo = "rolio"

[server]
addr = "http://127.0.0.1:7635"

[docs]
path = "/internal-docs"
`)

	cfg, err := LoadCLI(path)
	if err != nil {
		t.Fatalf("LoadCLI() error = %v", err)
	}
	if cfg.Docs.Path != "internal-docs" {
		t.Fatalf("LoadCLI().Docs.Path = %q, want internal-docs", cfg.Docs.Path)
	}
}

func TestLoadCLIRejectsBackendConfig(t *testing.T) {
	path := writeConfig(t, "settings.toml", `
repo = "rolio"

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

func TestLoadCLIRejectsUnsupportedAuthMode(t *testing.T) {
	path := writeConfig(t, "settings.toml", `
repo = "rolio"

[server]
addr = "http://127.0.0.1:7635"

[auth]
mode = "jwt"
`)

	_, err := LoadCLI(path)
	if err == nil {
		t.Fatal("LoadCLI() error = nil, want auth rejection")
	}
}

func TestLoadCLIRejectsUnsupportedMaterialize(t *testing.T) {
	path := writeConfig(t, "settings.toml", `
repo = "rolio"

[server]
addr = "http://127.0.0.1:7635"

[cache]
materialize = "instant"
`)

	_, err := LoadCLI(path)
	if err == nil {
		t.Fatal("LoadCLI() error = nil, want materialize rejection")
	}
}

func TestLoadMountsConfig(t *testing.T) {
	path := writeConfig(t, "mounts.toml", `
version = 1

[[mounts]]
local = "docs"
remote = "repo://self/docs"
mode = "writable"
source = "default"

[[mounts]]
local = "docs/gotchas/openai-go"
remote = "docset://openai-go/v3/gotchas"
mode = "readonly"
source = "search"
`)

	cfg, err := LoadMounts(path)
	if err != nil {
		t.Fatalf("LoadMounts() error = %v", err)
	}
	if cfg.Version != 1 || len(cfg.Mounts) != 2 {
		t.Fatalf("LoadMounts() = %+v, want version 1 and two mounts", cfg)
	}
	if cfg.Mounts[0].Local != "docs" || cfg.Mounts[0].Remote != "repo://self/docs" || cfg.Mounts[0].Mode != "writable" {
		t.Fatalf("mount[0] = %+v, want writable docs self repo mount", cfg.Mounts[0])
	}
	if cfg.Mounts[1].Local != "docs/gotchas/openai-go" || cfg.Mounts[1].Mode != "readonly" || cfg.Mounts[1].Source != "search" {
		t.Fatalf("mount[1] = %+v, want readonly search mount", cfg.Mounts[1])
	}
}

func TestLoadMountsRejectsInvalidMode(t *testing.T) {
	path := writeConfig(t, "mounts.toml", `
[[mounts]]
local = "docs"
remote = "repo://self/docs"
mode = "admin"
`)

	_, err := LoadMounts(path)
	if err == nil {
		t.Fatal("LoadMounts() error = nil, want invalid mode rejection")
	}
}

func TestDefaultMountsFromCLIConfig(t *testing.T) {
	cfg := CLIConfig{Repo: "rolio", Docs: Docs{Path: "docs"}}
	mounts := DefaultMounts(cfg)
	if mounts.Version != 1 || len(mounts.Mounts) != 1 {
		t.Fatalf("DefaultMounts() = %+v, want one mount", mounts)
	}
	m := mounts.Mounts[0]
	if m.Local != "docs" || m.Remote != "repo://self/docs" || m.Mode != "writable" || m.Source != "default" {
		t.Fatalf("default mount = %+v, want docs writable self repo mount", m)
	}
}

func TestLoadServerConfigExpandsEnv(t *testing.T) {
	t.Setenv("ROLIO_POSTGRES_DSN", "postgres://user:pass@localhost/rolio")

	path := writeConfig(t, "server.toml", `
addr = ":7635"

[backend]
type = "postgres"

[backend.postgres]
dsn = "${ROLIO_POSTGRES_DSN}"
schema = "public"
`)

	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	if cfg.Addr != ":7635" || cfg.Backend.Type != "postgres" {
		t.Fatalf("LoadServer() = %+v, want addr and postgres backend", cfg)
	}
	if cfg.Backend.Postgres.DSN != "postgres://user:pass@localhost/rolio" {
		t.Fatalf("dsn = %q, want expanded env", cfg.Backend.Postgres.DSN)
	}
	if cfg.Registry.RefreshInterval != "10s" {
		t.Fatalf("registry refresh interval = %q, want default 10s", cfg.Registry.RefreshInterval)
	}
}

func TestLoadServerConfigParsesPostgresFileTable(t *testing.T) {
	path := writeConfig(t, "server.toml", `
addr = ":7635"

[backend]
type = "postgres"

[backend.postgres]
dsn = "postgres://localhost/rolio"
schema = "public"
nodes_table = "my_nodes"
content_table = "my_content"

[backend.postgres.files]
path_column = "file_path"
kind_column = "node_type"
size_column = "byte_size"
mtime_column = "changed_at"

[registry]
refresh_interval = "30s"
`)

	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	pg := cfg.Backend.Postgres
	if pg.NodesTable != "my_nodes" || pg.ContentTable != "my_content" {
		t.Fatalf("postgres tables = %q/%q, want my_nodes/my_content", pg.NodesTable, pg.ContentTable)
	}
	files := pg.Files
	if files.PathColumn != "file_path" || files.KindColumn != "node_type" ||
		files.SizeColumn != "byte_size" || files.MTimeColumn != "changed_at" {
		t.Fatalf("postgres files = %+v, want custom mapping", files)
	}
	if cfg.Registry.RefreshInterval != "30s" {
		t.Fatalf("registry refresh interval = %q, want 30s", cfg.Registry.RefreshInterval)
	}
}

func TestLoadServerRejectsLegacyRegistrySections(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "repos",
			content: `
addr = ":7635"

[[repos]]
name = "rolio"

[backend]
type = "doc_postgres"

[backend.postgres]
dsn = "postgres://localhost/rolio"
`,
			wantErr: "server config repos are no longer supported",
		},
		{
			name: "docs",
			content: `
addr = ":7635"

[[docs]]
name = "shared"

[backend]
type = "doc_postgres"

[backend.postgres]
dsn = "postgres://localhost/rolio"
`,
			wantErr: "server config docs namespaces are no longer supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, "server.toml", tt.content)
			_, err := LoadServer(path)
			if err == nil {
				t.Fatal("LoadServer() error = nil, want legacy section rejection")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadServer() error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}
