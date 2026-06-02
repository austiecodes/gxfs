package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/austiecodes/gxfs/internal/config"
	"github.com/zeromicro/go-zero/rest/router"
)

func writeServerConfig(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write server config: %v", err)
	}
	return path
}

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

func TestPostgresConfigFromServerDefaultsFileTable(t *testing.T) {
	cfg := postgresConfigFromServer(config.ServerConfig{
		Backend: config.BackendConfig{
			Type: "doc_postgres",
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

func TestAPIRoutesRegistersRepoPost(t *testing.T) {
	routes := apiRoutes(http.NotFoundHandler())
	for _, route := range routes {
		if route.Method == http.MethodPost && route.Path == "/v1/repos" {
			return
		}
	}
	t.Fatal("apiRoutes() missing POST /v1/repos")
}

func TestAPIRoutesRegistersUsageEventsPost(t *testing.T) {
	routes := apiRoutes(http.NotFoundHandler())
	for _, route := range routes {
		if route.Method == http.MethodPost && route.Path == "/v1/usage-events" {
			return
		}
	}
	t.Fatal("apiRoutes() missing POST /v1/usage-events")
}

func TestAPIRoutesRegistersMountSources(t *testing.T) {
	routes := apiRoutes(http.NotFoundHandler())
	for _, route := range routes {
		if route.Method == http.MethodGet && route.Path == "/v1/mount-sources" {
			return
		}
	}
	t.Fatal("apiRoutes() missing GET /v1/mount-sources")
}

func TestAPIRoutesDispatchEscapedSlashRepoNames(t *testing.T) {
	tests := []string{"/v1/repos/ls?repo=github.com%2Faustiecodes%2Fxxxx"}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			called := false
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			rt := router.NewRouter()
			for _, route := range apiRoutes(handler) {
				if err := rt.Handle(route.Method, route.Path, route.Handler); err != nil {
					t.Fatalf("register route %s %s: %v", route.Method, route.Path, err)
				}
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			rec := httptest.NewRecorder()

			rt.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent || !called {
				t.Fatalf("route status = %d called = %v, want dispatch to handler", rec.Code, called)
			}
		})
	}
}

func TestAPIRoutesRegistersDocsNamespaceRoutes(t *testing.T) {
	routes := apiRoutes(http.NotFoundHandler())
	want := map[string]bool{
		http.MethodGet + " /v1/docs/:op":    false,
		http.MethodPut + " /v1/docs/:op":    false,
		http.MethodDelete + " /v1/docs/:op": false,
	}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Fatalf("apiRoutes() missing %s", key)
		}
	}
}

func TestLoadServerConfigUsesInfraOnlyBackend(t *testing.T) {
	path := writeServerConfig(t, "server.toml", `
addr = ":7635"

[backend]
type = "doc_postgres"

[backend.postgres]
dsn = "postgres://localhost/gxfs"
schema = "public"
cache_ttl = "5m"

[registry]
refresh_interval = "15s"
`)

	cfg, err := config.LoadServer(path)
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	if cfg.Addr != ":7635" || cfg.Backend.Type != "doc_postgres" {
		t.Fatalf("LoadServer() = %+v, want addr and doc_postgres backend", cfg)
	}
	if cfg.Backend.Postgres.DSN != "postgres://localhost/gxfs" ||
		cfg.Backend.Postgres.Schema != "public" ||
		cfg.Backend.Postgres.CacheTTL != "5m" {
		t.Fatalf("postgres backend = %+v, want parsed DSN/schema/cache", cfg.Backend.Postgres)
	}
	if cfg.Registry.RefreshInterval != "15s" {
		t.Fatalf("registry refresh interval = %q, want 15s", cfg.Registry.RefreshInterval)
	}
}

func TestLoadServerConfigRejectsMissingBackend(t *testing.T) {
	path := writeServerConfig(t, "server.toml", `addr = ":7635"`)

	_, err := config.LoadServer(path)
	if err == nil {
		t.Fatal("LoadServer() error = nil, want backend validation error")
	}
	if !strings.Contains(err.Error(), "backend.type is required") {
		t.Fatalf("LoadServer() error = %q, want backend.type validation", err.Error())
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

func TestCollectGCTargetsUsesInfraBackendOnly(t *testing.T) {
	cfg := config.ServerConfig{
		Backend: config.BackendConfig{
			Type: "doc_postgres",
			Postgres: config.PostgresConfig{
				DSN:    "postgres://user:pass@host1:5432/db1",
				Schema: "public",
			},
		},
	}

	targets := collectGCTargets(cfg)
	if len(targets) != 1 {
		t.Fatalf("collectGCTargets() returned %d targets, want 1", len(targets))
	}
	if got := targets[0].label(); got != "postgres://host1:5432/db1/public" {
		t.Fatalf("target label = %q, want redacted target", got)
	}
}

func TestCollectGCTargetsSkipsPathCentricBackend(t *testing.T) {
	cfg := config.ServerConfig{
		Backend: config.BackendConfig{
			Type: "postgres",
			Postgres: config.PostgresConfig{
				DSN:    "postgres://user:pass@host1:5432/db1",
				Schema: "public",
			},
		},
	}

	targets := collectGCTargets(cfg)
	if len(targets) != 0 {
		t.Fatalf("collectGCTargets() returned %+v, want none for path-centric postgres", targets)
	}
}

func TestStorageTargetLabel(t *testing.T) {
	target := storageTarget{
		dsn:    "postgres://secret:password@prod-db:5432/gxfs",
		schema: "tenant1",
	}

	label := target.label()

	if strings.Contains(label, "secret") || strings.Contains(label, "password") {
		t.Errorf("label %q contains credentials", label)
	}
	if !strings.Contains(label, "tenant1") {
		t.Errorf("label %q missing schema", label)
	}
}
