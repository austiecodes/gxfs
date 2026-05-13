//go:build e2e

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGXFSPostgresServerCLI(t *testing.T) {
	requireDocker(t)

	repoRoot := repositoryRoot(t)
	tmp := t.TempDir()

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-e2e-%d-%d", os.Getpid(), time.Now().UnixNano())
	startPostgres(t, containerName, pgPort)
	seedPostgres(t, containerName)

	cliPath := filepath.Join(tmp, "gxfs")
	serverPath := filepath.Join(tmp, "gxfs-server")
	buildBinary(t, repoRoot, cliPath, "./cmd/gxfs")
	buildBinary(t, repoRoot, serverPath, "./cmd/gxfs-server")

	serverPort := freePort(t)
	serverConfig := filepath.Join(tmp, "conf", "server.toml")
	os.MkdirAll(filepath.Join(tmp, "conf"), 0o755)
	writeFile(t, serverConfig, serverConfigText(serverPort, pgPort))

	startServer(t, repoRoot, serverPath, serverConfig, serverPort)

	cliConfig := filepath.Join(tmp, ".gxfs", "settings.toml")
	cliMounts := filepath.Join(tmp, ".gxfs", "mounts.toml")
	os.MkdirAll(filepath.Join(tmp, ".gxfs"), 0o755)
	writeFile(t, cliConfig, cliConfigText(serverPort))
	writeFile(t, cliMounts, cliMountsText())

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "config doctor reads cli config",
			args: []string{"config", "doctor"},
			want: "Repo: e2e-test\n",
		},
		{
			name: "ls lists root through server and postgres",
			args: []string{"ls", "/"},
			want: "docs/\n",
		},
		{
			name: "ls hides hidden files by default",
			args: []string{"ls", "/docs"},
			want: "api/\nreadme.md\n",
		},
		{
			name: "ls all shows hidden postgres rows",
			args: []string{"ls", "-a", "/docs"},
			want: ".secret.md\napi/\nreadme.md\n",
		},
		{
			name: "cat returns exact content",
			args: []string{"cat", "/docs/readme.md"},
			want: "# GXFS Docs\nThis document mentions Adapter.\n",
		},
		{
			name: "grep searches postgres-backed content",
			args: []string{"grep", "Adapter", "/"},
			want: "/docs/readme.md:2:This document mentions Adapter.\n",
		},
		{
			name: "find walks the synthesized tree",
			args: []string{"find", "/", "--name", "*.md"},
			want: "/docs/api/reference.md\n/docs/readme.md\n",
		},
		{
			name: "tree renders through the backend",
			args: []string{"tree", "/", "-L", "2"},
			want: "/\n  docs/\n    api/\n    readme.md\n",
		},
		{
			name: "stat returns file metadata",
			args: []string{"stat", "--terse", "/docs/readme.md"},
			want: "/docs/readme.md\tfile\t42\t2026-01-02T00:00:00Z\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCLI(t, repoRoot, cliPath, cliConfig, tt.args...)
			if got != tt.want {
				t.Fatalf("gxfs %s output = %q, want %q", strings.Join(tt.args, " "), got, tt.want)
			}
		})
	}

	// Write/Edit/Delete tests (sequential, build on each other)
	t.Run("write creates new file", func(t *testing.T) {
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/test-write.md", "hello world")
		got := runCLI(t, repoRoot, cliPath, cliConfig, "cat", "/docs/test-write.md")
		if got != "hello world" {
			t.Fatalf("cat after write = %q, want %q", got, "hello world")
		}
	})

	t.Run("write creates parent dirs", func(t *testing.T) {
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/deep/nested/dir/file.txt", "deep content")
		got := runCLI(t, repoRoot, cliPath, cliConfig, "ls", "/docs/deep/nested")
		if got != "dir/\n" {
			t.Fatalf("ls after deep write = %q, want %q", got, "dir/\n")
		}
	})

	t.Run("edit replaces first occurrence", func(t *testing.T) {
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/test-edit.md", "hello world\nhello go\n")
		runCLI(t, repoRoot, cliPath, cliConfig, "edit", "/docs/test-edit.md", "--old", "hello", "--new", "hi")
		got := runCLI(t, repoRoot, cliPath, cliConfig, "cat", "/docs/test-edit.md")
		if got != "hi world\nhello go\n" {
			t.Fatalf("edit first = %q, want %q", got, "hi world\nhello go\n")
		}
	})

	t.Run("edit all replaces all occurrences", func(t *testing.T) {
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/test-edit-all.md", "aaa bbb aaa\n")
		runCLI(t, repoRoot, cliPath, cliConfig, "edit", "/docs/test-edit-all.md", "--old", "aaa", "--new", "ccc", "--all")
		got := runCLI(t, repoRoot, cliPath, cliConfig, "cat", "/docs/test-edit-all.md")
		if got != "ccc bbb ccc\n" {
			t.Fatalf("edit all = %q, want %q", got, "ccc bbb ccc\n")
		}
	})

	t.Run("delete removes file", func(t *testing.T) {
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/test-del.md", "to be deleted")
		runCLI(t, repoRoot, cliPath, cliConfig, "delete", "/docs/test-del.md")
		got := runCLI(t, repoRoot, cliPath, cliConfig, "ls", "/docs")
		if strings.Contains(got, "test-del.md") {
			t.Fatalf("file still visible after delete: %q", got)
		}
	})

	t.Run("delete removes directory recursively", func(t *testing.T) {
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/test-dir/child.txt", "child")
		runCLI(t, repoRoot, cliPath, cliConfig, "delete", "/docs/test-dir")
		got := runCLI(t, repoRoot, cliPath, cliConfig, "ls", "/docs")
		if strings.Contains(got, "test-dir") {
			t.Fatalf("dir still visible after recursive delete: %q", got)
		}
	})

	t.Run("sync push uploads local docs and writes manifest", func(t *testing.T) {
		projectDir := filepath.Join(tmp, "sync-project")
		if err := os.MkdirAll(filepath.Join(projectDir, ".gxfs"), 0o755); err != nil {
			t.Fatalf("mkdir sync project config: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(projectDir, "docs", "sync"), 0o755); err != nil {
			t.Fatalf("mkdir sync docs: %v", err)
		}
		syncConfig := filepath.Join(projectDir, ".gxfs", "settings.toml")
		syncMounts := filepath.Join(projectDir, ".gxfs", "mounts.toml")
		writeFile(t, syncConfig, cliConfigText(serverPort))
		writeFile(t, syncMounts, cliMountsText())
		writeFile(t, filepath.Join(projectDir, "docs", "sync", "a.md"), "synced alpha")

		got := runCLIInDir(t, projectDir, cliPath, syncConfig, "sync", "push", "docs")
		if !strings.Contains(got, "pushed 1 file") || !strings.Contains(got, "updated .gxfs/manifest.toml") {
			t.Fatalf("sync push output = %q, want pushed count and manifest update", got)
		}
		cat := runCLIInDir(t, projectDir, cliPath, syncConfig, "cat", "/docs/sync/a.md")
		if cat != "synced alpha" {
			t.Fatalf("cat after sync push = %q, want synced alpha", cat)
		}
		manifest, err := os.ReadFile(filepath.Join(projectDir, ".gxfs", "manifest.toml"))
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		if !strings.Contains(string(manifest), `local = 'docs/sync/a.md'`) {
			t.Fatalf("manifest missing synced entry: %s", manifest)
		}
	})

	t.Run("sync pull materializes remote docs and writes manifest", func(t *testing.T) {
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/pull/a.md", "pulled alpha")

		projectDir := filepath.Join(tmp, "pull-project")
		if err := os.MkdirAll(filepath.Join(projectDir, ".gxfs"), 0o755); err != nil {
			t.Fatalf("mkdir pull project config: %v", err)
		}
		pullConfig := filepath.Join(projectDir, ".gxfs", "settings.toml")
		pullMounts := filepath.Join(projectDir, ".gxfs", "mounts.toml")
		writeFile(t, pullConfig, cliConfigText(serverPort))
		writeFile(t, pullMounts, cliMountsText())

		got := runCLIInDir(t, projectDir, cliPath, pullConfig, "sync", "pull", "docs/pull", "--materialize")
		if !strings.Contains(got, "pulled 1 file") || !strings.Contains(got, "materialized 1 file") {
			t.Fatalf("sync pull output = %q, want pulled/materialized count", got)
		}
		materialized := readFile(t, filepath.Join(projectDir, "docs", "pull", "a.md"))
		if materialized != "pulled alpha" {
			t.Fatalf("materialized file = %q, want pulled alpha", materialized)
		}
		manifest, err := os.ReadFile(filepath.Join(projectDir, ".gxfs", "manifest.toml"))
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		if !strings.Contains(string(manifest), `local = 'docs/pull/a.md'`) || !strings.Contains(string(manifest), `materialized = true`) {
			t.Fatalf("manifest missing materialized pull entry: %s", manifest)
		}
	})

	t.Run("materialize refresh and dematerialize commands update manifest", func(t *testing.T) {
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/materialize/a.md", "materialize alpha")

		projectDir := filepath.Join(tmp, "materialize-project")
		if err := os.MkdirAll(filepath.Join(projectDir, ".gxfs"), 0o755); err != nil {
			t.Fatalf("mkdir materialize project config: %v", err)
		}
		matConfig := filepath.Join(projectDir, ".gxfs", "settings.toml")
		matMounts := filepath.Join(projectDir, ".gxfs", "mounts.toml")
		writeFile(t, matConfig, cliConfigText(serverPort))
		writeFile(t, matMounts, cliMountsText())

		got := runCLIInDir(t, projectDir, cliPath, matConfig, "refresh", "docs/materialize")
		if !strings.Contains(got, "refreshed 1 file") || strings.Contains(got, "materialized") {
			t.Fatalf("refresh output = %q, want refreshed only", got)
		}
		if _, err := os.Stat(filepath.Join(projectDir, "docs", "materialize", "a.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file after refresh stat error = %v, want not exist", err)
		}

		got = runCLIInDir(t, projectDir, cliPath, matConfig, "materialize", "docs/materialize")
		if !strings.Contains(got, "materialized 1 file") {
			t.Fatalf("materialize output = %q, want materialized count", got)
		}
		materialized := readFile(t, filepath.Join(projectDir, "docs", "materialize", "a.md"))
		if materialized != "materialize alpha" {
			t.Fatalf("materialized file = %q, want materialize alpha", materialized)
		}

		got = runCLIInDir(t, projectDir, cliPath, matConfig, "dematerialize", "docs/materialize")
		if !strings.Contains(got, "dematerialized 1 file") {
			t.Fatalf("dematerialize output = %q, want dematerialized count", got)
		}
		if _, err := os.Stat(filepath.Join(projectDir, "docs", "materialize", "a.md")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file after dematerialize stat error = %v, want not exist", err)
		}
		// Request root "docs/materialize" is preserved (only empty parents above root are cleaned)
		if _, err := os.Stat(filepath.Join(projectDir, "docs", "materialize")); err != nil {
			t.Fatalf("request root dir stat error = %v, want preserved", err)
		}
		manifest, err := os.ReadFile(filepath.Join(projectDir, ".gxfs", "manifest.toml"))
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		if !strings.Contains(string(manifest), `local = 'docs/materialize/a.md'`) || !strings.Contains(string(manifest), `materialized = false`) {
			t.Fatalf("manifest missing dematerialized entry: %s", manifest)
		}
	})
}

func TestGXFSPostgresAutoMigratesEmptyDatabase(t *testing.T) {
	requireDocker(t)

	repoRoot := repositoryRoot(t)
	tmp := t.TempDir()

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-e2e-migrate-%d-%d", os.Getpid(), time.Now().UnixNano())
	startPostgres(t, containerName, pgPort)

	cliPath := filepath.Join(tmp, "gxfs")
	serverPath := filepath.Join(tmp, "gxfs-server")
	buildBinary(t, repoRoot, cliPath, "./cmd/gxfs")
	buildBinary(t, repoRoot, serverPath, "./cmd/gxfs-server")

	serverPort := freePort(t)
	serverConfig := filepath.Join(tmp, "conf", "server.toml")
	os.MkdirAll(filepath.Join(tmp, "conf"), 0o755)
	writeFile(t, serverConfig, serverConfigText(serverPort, pgPort))

	startServer(t, repoRoot, serverPath, serverConfig, serverPort)

	cliConfig := filepath.Join(tmp, ".gxfs", "settings.toml")
	cliMounts := filepath.Join(tmp, ".gxfs", "mounts.toml")
	os.MkdirAll(filepath.Join(tmp, ".gxfs"), 0o755)
	writeFile(t, cliConfig, cliConfigText(serverPort))
	writeFile(t, cliMounts, cliMountsText())

	runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/auto/migrated.md", "created by migration")
	got := runCLI(t, repoRoot, cliPath, cliConfig, "cat", "/docs/auto/migrated.md")
	if got != "created by migration" {
		t.Fatalf("cat after auto-migrated write = %q, want %q", got, "created by migration")
	}
}

func TestGXFSPostgresServerRoutesMultipleRepos(t *testing.T) {
	requireDocker(t)

	repoRoot := repositoryRoot(t)
	tmp := t.TempDir()

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-e2e-multirepo-%d-%d", os.Getpid(), time.Now().UnixNano())
	startPostgres(t, containerName, pgPort)

	cliPath := filepath.Join(tmp, "gxfs")
	serverPath := filepath.Join(tmp, "gxfs-server")
	buildBinary(t, repoRoot, cliPath, "./cmd/gxfs")
	buildBinary(t, repoRoot, serverPath, "./cmd/gxfs-server")

	serverPort := freePort(t)
	serverConfig := filepath.Join(tmp, "conf", "server.toml")
	os.MkdirAll(filepath.Join(tmp, "conf"), 0o755)
	writeFile(t, serverConfig, multiRepoServerConfigText(serverPort, pgPort))

	startServer(t, repoRoot, serverPath, serverConfig, serverPort)

	alphaConfig := filepath.Join(tmp, "alpha", ".gxfs", "settings.toml")
	alphaMounts := filepath.Join(tmp, "alpha", ".gxfs", "mounts.toml")
	betaConfig := filepath.Join(tmp, "beta", ".gxfs", "settings.toml")
	betaMounts := filepath.Join(tmp, "beta", ".gxfs", "mounts.toml")
	os.MkdirAll(filepath.Dir(alphaConfig), 0o755)
	os.MkdirAll(filepath.Dir(betaConfig), 0o755)
	writeFile(t, alphaConfig, cliConfigTextForRepo(serverPort, "alpha"))
	writeFile(t, alphaMounts, cliMountsText())
	writeFile(t, betaConfig, cliConfigTextForRepo(serverPort, "beta"))
	writeFile(t, betaMounts, cliMountsText())

	runCLI(t, repoRoot, cliPath, alphaConfig, "write", "/docs/alpha.md", "alpha content")
	runCLI(t, repoRoot, cliPath, betaConfig, "write", "/docs/beta.md", "beta content")

	alphaCat := runCLI(t, repoRoot, cliPath, alphaConfig, "cat", "/docs/alpha.md")
	if alphaCat != "alpha content" {
		t.Fatalf("alpha cat = %q, want alpha content", alphaCat)
	}
	betaCat := runCLI(t, repoRoot, cliPath, betaConfig, "cat", "/docs/beta.md")
	if betaCat != "beta content" {
		t.Fatalf("beta cat = %q, want beta content", betaCat)
	}

	alphaLS := runCLI(t, repoRoot, cliPath, alphaConfig, "ls", "/docs")
	if strings.Contains(alphaLS, "beta.md") || !strings.Contains(alphaLS, "alpha.md") {
		t.Fatalf("alpha ls = %q, want alpha.md only", alphaLS)
	}
	betaLS := runCLI(t, repoRoot, cliPath, betaConfig, "ls", "/docs")
	if strings.Contains(betaLS, "alpha.md") || !strings.Contains(betaLS, "beta.md") {
		t.Fatalf("beta ls = %q, want beta.md only", betaLS)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

func requireDocker(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker is required for e2e tests: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("docker daemon is required for e2e tests: %v: %s", err, strings.TrimSpace(string(output)))
	}
}

func startPostgres(t *testing.T, containerName string, port int) {
	t.Helper()

	image := os.Getenv("GXFS_E2E_POSTGRES_IMAGE")
	if image == "" {
		image = "postgres:18-alpine"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	output, err := run(ctx, "", nil,
		"docker", "run", "-d", "--rm",
		"--name", containerName,
		"-e", "POSTGRES_USER=gxfs",
		"-e", "POSTGRES_PASSWORD=gxfs",
		"-e", "POSTGRES_DB=gxfs",
		"-p", fmt.Sprintf("127.0.0.1:%d:5432", port),
		image,
	)
	if err != nil {
		t.Fatalf("start postgres container: %v: %s", err, output)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = run(ctx, "", nil, "docker", "rm", "-f", containerName)
	})

	waitForPostgres(t, containerName)
}

func waitForPostgres(t *testing.T, containerName string) {
	t.Helper()

	deadline := time.Now().Add(90 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		output, err := run(ctx, "", nil,
			"docker", "exec", containerName,
			"psql", "-U", "gxfs", "-d", "gxfs", "-v", "ON_ERROR_STOP=1", "-c", "select 1",
		)
		cancel()
		if err == nil {
			return
		}
		last = output
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("postgres did not become ready: %s", last)
}

func seedPostgres(t *testing.T, containerName string) {
	t.Helper()

	const sql = `
create table vfs_nodes (
	path text primary key,
	kind text not null default 'file',
	size bigint not null default 0,
	updated_at timestamptz not null default now()
);

create table vfs_content (
	path text primary key references vfs_nodes(path) on delete cascade,
	content text not null default ''
);

create table vfs_repo_nodes (
	repo text not null,
	path text not null references vfs_nodes(path) on delete cascade,
	primary key (repo, path)
);

insert into vfs_nodes(path, kind, size, updated_at) values
	('/docs', 'dir', 0, '2026-01-01T00:00:00Z'),
	('/docs/api', 'dir', 0, '2026-01-01T00:00:00Z'),
	('/src', 'dir', 0, '2026-01-01T00:00:00Z'),
	('/README.md', 'file', 17, '2026-01-01T00:00:00Z'),
	('/docs/readme.md', 'file', 42, '2026-01-02T00:00:00Z'),
	('/docs/api/reference.md', 'file', 14, '2026-01-04T00:00:00Z'),
	('/docs/.secret.md', 'file', 12, '2026-01-05T00:00:00Z'),
	('/src/main.go', 'file', 28, '2026-01-03T00:00:00Z');

insert into vfs_content(path, content) values
	('/README.md', 'GXFS root readme' || chr(10)),
	('/docs/readme.md', '# GXFS Docs' || chr(10) || 'This document mentions Adapter.' || chr(10)),
	('/docs/api/reference.md', 'API reference' || chr(10)),
	('/docs/.secret.md', 'hidden docs' || chr(10)),
	('/src/main.go', 'package main' || chr(10) || 'func main() {}' || chr(10));

insert into vfs_repo_nodes(repo, path) values
	('e2e-test', '/docs'),
	('e2e-test', '/docs/api'),
	('e2e-test', '/src'),
	('e2e-test', '/README.md'),
	('e2e-test', '/docs/readme.md'),
	('e2e-test', '/docs/api/reference.md'),
	('e2e-test', '/docs/.secret.md'),
	('e2e-test', '/src/main.go');
`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, err := run(ctx, "", strings.NewReader(sql),
		"docker", "exec", "-i", containerName,
		"psql", "-U", "gxfs", "-d", "gxfs", "-v", "ON_ERROR_STOP=1",
	)
	if err != nil {
		t.Fatalf("seed postgres: %v: %s", err, output)
	}
}

func buildBinary(t *testing.T, repoRoot, outPath, pkg string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	output, err := run(ctx, repoRoot, nil, "go", "build", "-o", outPath, pkg)
	if err != nil {
		t.Fatalf("build %s: %v: %s", pkg, err, output)
	}
}

func startServer(t *testing.T, repoRoot, serverPath, configPath string, port int) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, serverPath)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GXFS_SERVER_CONFIG="+configPath)
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start gxfs-server: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	waitForServer(t, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), cmd, &output)
}

func waitForServer(t *testing.T, healthURL string, cmd *exec.Cmd, output *strings.Builder) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("gxfs-server exited before readiness: %s", output.String())
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err == nil {
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					cancel()
					return
				}
			}
		}
		cancel()
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("gxfs-server did not become ready: %s", output.String())
}

func runCLI(t *testing.T, repoRoot, cliPath, configPath string, args ...string) string {
	t.Helper()
	return runCLIInDir(t, repoRoot, cliPath, configPath, args...)
}

func runCLIInDir(t *testing.T, dir, cliPath, configPath string, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, err := runWithEnv(ctx, dir, append(os.Environ(), "GXFS_CONFIG="+configPath), nil, cliPath, args...)
	if err != nil {
		t.Fatalf("gxfs %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return output
}

func run(ctx context.Context, dir string, stdin io.Reader, name string, args ...string) (string, error) {
	return runWithEnv(ctx, dir, nil, stdin, name, args...)
}

func runWithEnv(ctx context.Context, dir string, env []string, stdin io.Reader, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func serverConfigText(serverPort, pgPort int) string {
	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	return fmt.Sprintf(`addr = "127.0.0.1:%d"

[[repos]]
name = "e2e-test"

[repos.backend]
type = "postgres"

[repos.backend.postgres]
dsn = %q
schema = "public"
nodes_table = "vfs_nodes"
content_table = "vfs_content"
repo_nodes_table = "vfs_repo_nodes"

[repos.backend.postgres.files]
path_column = "path"
kind_column = "kind"
size_column = "size"
mtime_column = "updated_at"
`, serverPort, dsn)
}

func multiRepoServerConfigText(serverPort, pgPort int) string {
	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	return fmt.Sprintf(`addr = "127.0.0.1:%d"

[[repos]]
name = "alpha"

[repos.backend]
type = "postgres"

[repos.backend.postgres]
dsn = %q
schema = "public"
nodes_table = "vfs_nodes"
content_table = "vfs_content"
repo_nodes_table = "vfs_repo_nodes"

[repos.backend.postgres.files]
path_column = "path"
kind_column = "kind"
size_column = "size"
mtime_column = "updated_at"

[[repos]]
name = "beta"

[repos.backend]
type = "postgres"

[repos.backend.postgres]
dsn = %q
schema = "public"
nodes_table = "vfs_nodes"
content_table = "vfs_content"
repo_nodes_table = "vfs_repo_nodes"

[repos.backend.postgres.files]
path_column = "path"
kind_column = "kind"
size_column = "size"
mtime_column = "updated_at"
`, serverPort, dsn, dsn)
}

func cliConfigText(serverPort int) string {
	return cliConfigTextForRepo(serverPort, "e2e-test")
}

func cliConfigTextForRepo(serverPort int, repo string) string {
	return fmt.Sprintf(`repo = %q
version = 1

[server]
addr = "http://127.0.0.1:%d"

[docs]
path = "docs"
`, repo, serverPort)
}

func cliMountsText() string {
	return `version = 1

[[mounts]]
local = "docs"
remote = "repo://self/docs"
mode = "writable"
source = "default"
`
}

func rootMountsText() string {
	return `version = 1

[[mounts]]
local = "."
remote = "repo://self/"
mode = "writable"
source = "default"
`
}

func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

// --- doc_postgres server config ---

func docPostgresServerConfigText(serverPort, pgPort int) string {
	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	return fmt.Sprintf(`addr = "127.0.0.1:%d"

[[repos]]
name = "e2e-test"

[repos.backend]
type = "doc_postgres"

[repos.backend.postgres]
dsn = %q
schema = "public"
nodes_table = "vfs_nodes"
content_table = "vfs_content"
repo_nodes_table = "vfs_repo_nodes"

[repos.backend.postgres.files]
path_column = "path"
kind_column = "kind"
size_column = "size"
mtime_column = "updated_at"
`, serverPort, dsn)
}

// TestDocPostgresServerE2E verifies that gxfs-server with type = "doc_postgres"
// correctly auto-migrates, backfills legacy data, and serves full CRUD through CLI.
// Also verifies backfill idempotency on restart and old postgres regression.
func TestDocPostgresServerE2E(t *testing.T) {
	requireDocker(t)

	repoRoot := repositoryRoot(t)
	tmp := t.TempDir()

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-doc-postgres-e2e-%d-%d", os.Getpid(), time.Now().UnixNano())
	startPostgres(t, containerName, pgPort)

	// Seed legacy data so backfill has something to migrate.
	seedPostgres(t, containerName)

	cliPath := filepath.Join(tmp, "gxfs")
	serverPath := filepath.Join(tmp, "gxfs-server")
	buildBinary(t, repoRoot, cliPath, "./cmd/gxfs")
	buildBinary(t, repoRoot, serverPath, "./cmd/gxfs-server")

	// --- Phase 1: Start server with doc_postgres, verify legacy data readable ---
	serverPort := freePort(t)
	serverConfig := filepath.Join(tmp, "conf", "server.toml")
	os.MkdirAll(filepath.Join(tmp, "conf"), 0o755)
	writeFile(t, serverConfig, docPostgresServerConfigText(serverPort, pgPort))

	startServer(t, repoRoot, serverPath, serverConfig, serverPort)

	cliConfig := filepath.Join(tmp, ".gxfs", "settings.toml")
	cliMounts := filepath.Join(tmp, ".gxfs", "mounts.toml")
	os.MkdirAll(filepath.Join(tmp, ".gxfs"), 0o755)
	writeFile(t, cliConfig, cliConfigText(serverPort))
	writeFile(t, cliMounts, cliMountsText())

	// Legacy data should be backfilled and readable.
	// CLI mount: local docs → remote /docs, but CLI paths are repo paths.
	// So /docs/readme.md is the repo path for the mounted file.
	t.Run("LegacyDataReadable", func(t *testing.T) {
		ls := runCLI(t, repoRoot, cliPath, cliConfig, "ls", "/docs")
		if !strings.Contains(ls, "readme.md") {
			t.Fatalf("ls /docs after backfill = %q, expected readme.md", ls)
		}
		cat := runCLI(t, repoRoot, cliPath, cliConfig, "cat", "/docs/readme.md")
		if cat == "" {
			t.Fatal("cat /docs/readme.md empty after backfill")
		}
	})

	// Full CRUD through doc_postgres.
	t.Run("CRUD", func(t *testing.T) {
		// Write new file under /docs.
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/new-file.txt", "hello doc_postgres")

		// Cat it back.
		got := runCLI(t, repoRoot, cliPath, cliConfig, "cat", "/docs/new-file.txt")
		if got != "hello doc_postgres" {
			t.Fatalf("cat /docs/new-file.txt = %q, want %q", got, "hello doc_postgres")
		}

		// Overwrite.
		runCLI(t, repoRoot, cliPath, cliConfig, "write", "/docs/new-file.txt", "updated content")
		got = runCLI(t, repoRoot, cliPath, cliConfig, "cat", "/docs/new-file.txt")
		if got != "updated content" {
			t.Fatalf("cat after overwrite = %q, want %q", got, "updated content")
		}

		// Delete.
		runCLI(t, repoRoot, cliPath, cliConfig, "rm", "/docs/new-file.txt")
	})

	// Search should work (doc_postgres uses content_search tsvector).
	t.Run("Search", func(t *testing.T) {
		out := runCLI(t, repoRoot, cliPath, cliConfig, "search", "GXFS")
		if out == "" {
			t.Fatal("search GXFS returned nothing")
		}
	})

	// --- Phase 2: Restart server, verify backfill idempotency ---
	// Use a separate subtest with its own server on a new port.
	t.Run("RestartIdempotent", func(t *testing.T) {
		restartPort := freePort(t)
		restartConfig := filepath.Join(tmp, "conf", "restart.toml")
		writeFile(t, restartConfig, docPostgresServerConfigText(restartPort, pgPort))

		startServer(t, repoRoot, serverPath, restartConfig, restartPort)

		restartCLIConfig := filepath.Join(tmp, ".gxfs", "restart-settings.toml")
		writeFile(t, restartCLIConfig, cliConfigText(restartPort))

		// Data should still be intact after restart (backfill idempotent).
		cat := runCLI(t, repoRoot, cliPath, restartCLIConfig, "cat", "/docs/readme.md")
		if cat == "" {
			t.Fatal("cat /docs/readme.md empty after restart")
		}

		// Write should still work.
		runCLI(t, repoRoot, cliPath, restartCLIConfig, "write", "/docs/post-restart.txt", "still works")
		got := runCLI(t, repoRoot, cliPath, restartCLIConfig, "cat", "/docs/post-restart.txt")
		if got != "still works" {
			t.Fatalf("post-restart write = %q, want %q", got, "still works")
		}
	})

	// --- Phase 3: old postgres regression ---
	t.Run("PostgresRegression", func(t *testing.T) {
		regPort := freePort(t)
		regConfig := filepath.Join(tmp, "conf", "regression.toml")
		writeFile(t, regConfig, serverConfigText(regPort, pgPort))

		startServer(t, repoRoot, serverPath, regConfig, regPort)

		regCLIConfig := filepath.Join(tmp, ".gxfs", "reg-settings.toml")
		writeFile(t, regCLIConfig, cliConfigText(regPort))

		// Old postgres should still work (same mounts → paths under /docs).
		cat := runCLI(t, repoRoot, cliPath, regCLIConfig, "cat", "/docs/readme.md")
		if cat == "" {
			t.Fatal("old postgres cat /docs/readme.md empty")
		}

		runCLI(t, repoRoot, cliPath, regCLIConfig, "write", "/docs/regression-test.txt", "old adapter works")
		got := runCLI(t, repoRoot, cliPath, regCLIConfig, "cat", "/docs/regression-test.txt")
		if got != "old adapter works" {
			t.Fatalf("old postgres write = %q, want %q", got, "old adapter works")
		}
	})
}
