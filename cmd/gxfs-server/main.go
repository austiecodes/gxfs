package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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
	adapters := make(map[string]store.Adapter, len(cfg.Repos))
	for _, repo := range cfg.Repos {
		if _, exists := adapters[repo.Name]; exists {
			return nil, fmt.Errorf("duplicate repo %q", repo.Name)
		}
		adapter, err := adapterFromRepoConfig(ctx, repo)
		if err != nil {
			return nil, fmt.Errorf("repo %s: %w", repo.Name, err)
		}
		adapters[repo.Name] = adapter
	}
	return store.NewRegistry(adapters)
}

func adapterFromRepoConfig(ctx context.Context, repo config.RepoConfig) (store.Adapter, error) {
	switch repo.Backend.Type {
	case "postgres":
		return postgres.Connect(ctx, postgresConfigFromRepo(repo))
	default:
		return nil, fmt.Errorf("unsupported backend type: %s", repo.Backend.Type)
	}
}

func postgresConfigFromRepo(repo config.RepoConfig) postgres.Config {
	files := repo.Backend.Postgres.Files
	pg := repo.Backend.Postgres

	var cacheTTL time.Duration
	if pg.CacheTTL != "" {
		cacheTTL, _ = time.ParseDuration(pg.CacheTTL)
	}

	return postgres.Config{
		DSN:            pg.DSN,
		Schema:         pg.Schema,
		Repo:           repo.Name,
		NodesTable:     defaultString(pg.NodesTable, "vfs_nodes"),
		ContentTable:   defaultString(pg.ContentTable, "vfs_content"),
		RepoNodesTable: defaultString(pg.RepoNodesTable, "vfs_repo_nodes"),
		CacheTTL:       cacheTTL,
		Files: postgres.FileTableConfig{
			PathColumn:  defaultString(files.PathColumn, "path"),
			KindColumn:  defaultString(files.KindColumn, "kind"),
			SizeColumn:  defaultString(files.SizeColumn, "size"),
			MTimeColumn: defaultString(files.MTimeColumn, "updated_at"),
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
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	path := os.Getenv("GXFS_SERVER_CONFIG")
	if path == "" {
		path = "conf/server.toml"
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
	srv.AddRoute(rest.Route{Method: http.MethodDelete, Path: "/v1/cache", Handler: handler.ServeHTTP})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/v1/repos/:repo/:op", Handler: handler.ServeHTTP})
	srv.AddRoute(rest.Route{Method: http.MethodPut, Path: "/v1/repos/:repo/:op", Handler: handler.ServeHTTP})
	srv.AddRoute(rest.Route{Method: http.MethodDelete, Path: "/v1/repos/:repo/:op", Handler: handler.ServeHTTP})
	srv.Start()
}
