package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/zeromicro/go-zero/rest"

	"gxfs/internal/config"
	"gxfs/internal/server"
	"gxfs/internal/store"
	"gxfs/internal/store/postgres"
)

func splitAddr(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			host = "0.0.0.0"
			portText = strings.TrimPrefix(addr, ":")
		} else {
			return "", 0, fmt.Errorf("invalid addr %q: %w", addr, err)
		}
	}
	if host == "" {
		host = "0.0.0.0"
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portText, err)
	}
	return host, port, nil
}

func adapterFromServerConfig(ctx context.Context, cfg config.ServerConfig) (store.Adapter, error) {
	if len(cfg.Repos) != 1 {
		return nil, fmt.Errorf("exactly one repo is supported in this version")
	}

	repo := cfg.Repos[0]
	switch repo.Backend.Type {
	case "postgres":
		return postgres.Connect(ctx, postgresConfigFromRepo(repo))
	default:
		return nil, fmt.Errorf("unsupported backend type: %s", repo.Backend.Type)
	}
}

func postgresConfigFromRepo(repo config.RepoConfig) postgres.Config {
	files := repo.Backend.Postgres.Files
	return postgres.Config{
		DSN:    repo.Backend.Postgres.DSN,
		Schema: repo.Backend.Postgres.Schema,
		Files: postgres.FileTableConfig{
			Table:         defaultString(files.Table, "vfs_files"),
			PathColumn:    defaultString(files.PathColumn, "path"),
			ContentColumn: defaultString(files.ContentColumn, "content"),
			SizeColumn:    defaultString(files.SizeColumn, "size"),
			MTimeColumn:   defaultString(files.MTimeColumn, "updated_at"),
		},
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func main() {
	path := os.Getenv("GXFS_SERVER_CONFIG")
	if path == "" {
		path = "gxfs-server.toml"
	}
	cfg, err := config.LoadServer(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	host, port, err := splitAddr(cfg.Addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	adapter, err := adapterFromServerConfig(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	handler := server.NewHandler(adapter)

	srv := rest.MustNewServer(rest.RestConf{Host: host, Port: port})
	defer srv.Stop()

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/healthz", Handler: handler.ServeHTTP})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/v1/repos/:repo/:op", Handler: handler.ServeHTTP})
	srv.Start()
}
