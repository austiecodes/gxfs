package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"github.com/zeromicro/go-zero/rest"

	"github.com/austiecodes/gxfs/internal/config"
	"github.com/austiecodes/gxfs/internal/server"
	"github.com/austiecodes/gxfs/internal/store"
	"github.com/austiecodes/gxfs/internal/store/postgres"
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

func dynamicRegistryFromServerConfig(ctx context.Context, cfg config.ServerConfig, pool *pgxpool.Pool) (*server.DynamicRegistry, error) {
	if err := validateRepoBackendType(cfg.Backend.Type); err != nil {
		return nil, fmt.Errorf("default backend: %w", err)
	}

	baseCfg := postgresConfigFromServer(cfg)
	catalog := postgres.NewRegistry(pool, baseCfg)
	factory := func(_ context.Context, source server.DynamicSource) (store.Adapter, error) {
		sourceCfg := baseCfg
		sourceCfg.Repo = source.Name
		switch source.Kind {
		case store.SourceKindRepo:
			switch cfg.Backend.Type {
			case "postgres":
				return postgres.New(pool, sourceCfg), nil
			case "doc_postgres":
				return postgres.NewDocAdapter(pool, sourceCfg), nil
			default:
				return nil, fmt.Errorf("unsupported backend type: %s", cfg.Backend.Type)
			}
		case store.SourceKindDocs:
			return postgres.NewDocsNamespaceAdapter(pool, sourceCfg), nil
		default:
			return nil, fmt.Errorf("%w: %s", store.ErrNotSupported, source.Kind)
		}
	}
	return server.NewDynamicRegistry(ctx, catalog, catalog, factory)
}

func adapterFromRepoConfig(ctx context.Context, repo config.RepoConfig) (store.Adapter, error) {
	switch repo.Backend.Type {
	case "postgres":
		return postgres.Connect(ctx, postgresConfigFromRepo(repo))
	case "doc_postgres":
		return postgres.ConnectDoc(ctx, postgresConfigFromRepo(repo))
	default:
		return nil, fmt.Errorf("unsupported backend type: %s", repo.Backend.Type)
	}
}

func adapterFromDocsNamespaceConfig(ctx context.Context, docs config.DocsNamespaceConfig) (store.Adapter, error) {
	switch docs.Backend.Type {
	case "doc_postgres", "doc_namespace_postgres":
		return postgres.ConnectDocNamespace(ctx, postgresConfigFromDocsNamespace(docs))
	case "postgres":
		return nil, fmt.Errorf("unsupported backend type for docs namespace: %s (path-centric postgres is repo-only)", docs.Backend.Type)
	default:
		return nil, fmt.Errorf("unsupported backend type: %s", docs.Backend.Type)
	}
}

func validateRepoBackendType(backendType string) error {
	switch backendType {
	case "postgres", "doc_postgres":
		return nil
	default:
		return fmt.Errorf("unsupported backend type: %s", backendType)
	}
}

func validateDocsNamespaceBackendType(backendType string) error {
	switch backendType {
	case "doc_postgres", "doc_namespace_postgres":
		return nil
	case "postgres":
		return fmt.Errorf("unsupported backend type for docs namespace: %s (path-centric postgres is repo-only)", backendType)
	default:
		return fmt.Errorf("unsupported backend type: %s", backendType)
	}
}

func postgresConfigFromRepo(repo config.RepoConfig) postgres.Config {
	return postgresConfigFromBackend(repo.Name, repo.Backend)
}

func postgresConfigFromDocsNamespace(docs config.DocsNamespaceConfig) postgres.Config {
	return postgresConfigFromBackend(docs.Name, docs.Backend)
}

func postgresConfigFromServer(cfg config.ServerConfig) postgres.Config {
	return postgresConfigFromBackend("", cfg.Backend)
}

func postgresConfigFromBackend(scope string, backend config.BackendConfig) postgres.Config {
	files := backend.Postgres.Files
	pg := backend.Postgres

	var cacheTTL time.Duration
	if pg.CacheTTL != "" {
		cacheTTL, _ = time.ParseDuration(pg.CacheTTL)
	}

	return postgres.Config{
		DSN:            pg.DSN,
		Schema:         pg.Schema,
		Repo:           scope,
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

func apiRoutes(handler http.Handler) []rest.Route {
	routes := []rest.Route{
		{Method: http.MethodGet, Path: "/healthz", Handler: handler.ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/cache", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/repos", Handler: handler.ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/repos", Handler: handler.ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/usage-events", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/mount-sources", Handler: handler.ServeHTTP},
		// Collection routes
		{Method: http.MethodPost, Path: "/v1/collections", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/collections", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/collections/:name", Handler: handler.ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/collections/:name", Handler: handler.ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/collections/:name/members", Handler: handler.ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/collections/:name/members", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/collections/:name/docs", Handler: handler.ServeHTTP},
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		routes = append(routes,
			rest.Route{Method: method, Path: "/v1/repos/:op", Handler: handler.ServeHTTP},
			rest.Route{Method: method, Path: "/v1/docs/:op", Handler: handler.ServeHTTP},
		)
	}
	return routes
}

// redactDSN returns a safe-to-print version of a DSN with credentials stripped.
// For postgres://user:pass@host:port/dbname, returns postgres://host:port/dbname
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "<redacted>"
	}
	// Check if it looks like a valid postgres DSN
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "<redacted>"
	}
	// Strip credentials
	if u.User != nil {
		u.User = nil
	}
	return u.String()
}

// storageTarget represents a unique database target for GC.
type storageTarget struct {
	dsn    string
	schema string
}

// label returns a safe-to-print label for this target.
func (t storageTarget) label() string {
	return fmt.Sprintf("%s/%s", redactDSN(t.dsn), t.schema)
}

// collectGCTargets extracts the document storage target from infra-only server config.
func collectGCTargets(cfg config.ServerConfig) []storageTarget {
	seen := make(map[storageTarget]bool)
	var targets []storageTarget
	switch cfg.Backend.Type {
	case "doc_postgres", "doc_namespace_postgres":
		targets = appendGCTarget(targets, seen, cfg.Backend.Postgres)
	}
	return targets
}

func appendGCTarget(targets []storageTarget, seen map[storageTarget]bool, pg config.PostgresConfig) []storageTarget {
	if pg.DSN == "" {
		return targets
	}
	t := storageTarget{dsn: pg.DSN, schema: pg.Schema}
	if seen[t] {
		return targets
	}
	seen[t] = true
	return append(targets, t)
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cmd := newRootCommand()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gxfs-server",
		Short: "GXFS virtual filesystem server",
		Long:  "GXFS server provides a REST API for virtual filesystem content backed by PostgreSQL.",
		Run: func(cmd *cobra.Command, args []string) {
			runServer()
		},
	}

	cmd.AddCommand(newGCCommand())
	return cmd
}

func newGCCommand() *cobra.Command {
	var dryRun bool
	var force bool
	var graceHours int
	var limit int
	var configPath string

	cmd := &cobra.Command{
		Use:   "gc run",
		Short: "Run orphan document garbage collection",
		Long: `Garbage collect orphan documents that have no references in repo_paths or collections.

Orphan documents are those that:
  - Have no references in gxfs_repo_paths
  - Have no references in gxfs_collection_docs
  - Were last updated more than the grace period ago (to protect in-progress creates)

By default, runs in dry-run mode to preview candidates without deleting.
Use --force to actually delete.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && force {
				return fmt.Errorf("cannot use both --dry-run and --force")
			}
			// Default to dry-run if neither flag is set
			if !force {
				dryRun = true
			}

			path := configPath
			if path == "" {
				path = os.Getenv("GXFS_SERVER_CONFIG")
			}
			if path == "" {
				path = "conf/server.toml"
			}

			cfg, err := config.LoadServer(path)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			targets := collectGCTargets(cfg)
			if len(targets) == 0 {
				return fmt.Errorf("no doc_postgres storage targets configured")
			}

			req := postgres.GCRequest{
				DryRun:     dryRun,
				GraceHours: graceHours,
				Limit:      limit,
			}

			var totalCount int
			for _, target := range targets {
				result, err := postgres.GCWithPool(context.Background(), target.dsn, target.schema, req)
				if err != nil {
					return fmt.Errorf("gc %s: %w", target.label(), err)
				}
				totalCount += result.Count

				if dryRun {
					fmt.Fprintf(cmd.OutOrStdout(), "Target %s: found %d orphan document(s)\n", target.label(), result.Count)
					if len(result.Candidates) > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "  Sample candidates (up to %d):\n", limit)
						for _, c := range result.Candidates {
							fmt.Fprintf(cmd.OutOrStdout(), "    - id: %s\n", c.ID)
							if c.Title != "" {
								fmt.Fprintf(cmd.OutOrStdout(), "      title: %s\n", c.Title)
							}
							if c.LegacyPath != "" {
								fmt.Fprintf(cmd.OutOrStdout(), "      legacy_path: %s\n", c.LegacyPath)
							}
						}
					}
				}
			}

			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "\nTotal: %d orphan document(s) across %d storage target(s)\n", totalCount, len(targets))
				fmt.Fprintf(cmd.OutOrStdout(), "Run with --force to delete these documents.\n")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted %d orphan document(s) across %d storage target(s)\n", totalCount, len(targets))
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview candidates without deleting (default)")
	cmd.Flags().BoolVar(&force, "force", false, "Actually delete orphan documents")
	cmd.Flags().IntVar(&graceHours, "grace-hours", 1, "Grace period in hours (protects in-progress creates)")
	cmd.Flags().IntVar(&limit, "limit", 10, "Max candidates to show in dry-run")
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to server config file (default: $GXFS_SERVER_CONFIG or conf/server.toml)")

	return cmd
}

func runServer() {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pgCfg := postgresConfigFromServer(cfg)
	pool, err := pgxpool.New(ctx, pgCfg.DSN)
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("connect postgres: %w", err))
		os.Exit(1)
	}
	defer pool.Close()

	if err := ensurePostgresSchema(ctx, pool, pgCfg); err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("migrate postgres schema: %w", err))
		os.Exit(1)
	}
	if cfg.Backend.Type == "doc_postgres" {
		result, err := postgres.BackfillDocs(ctx, pool, pgCfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, fmt.Errorf("doc backfill: %w", err))
			os.Exit(1)
		}
		slog.Info("doc adapter backfill",
			"docs_inserted", result.DocsInserted,
			"paths_inserted", result.PathsInserted,
			"hashes_computed", result.HashesComputed,
		)
	}

	adapter, err := dynamicRegistryFromServerConfig(ctx, cfg, pool)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	refreshInterval, err := time.ParseDuration(cfg.Registry.RefreshInterval)
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("invalid registry.refresh_interval: %w", err))
		os.Exit(1)
	}
	adapter.StartRefreshLoop(ctx, refreshInterval)

	var handler http.Handler
	if cfg.Backend.Type == "doc_postgres" {
		collectionMgr := postgres.NewCollectionAdapter(pool, pgCfg.Schema)
		handler = server.NewHandlerWithCollections(adapter, nil, collectionMgr)
	} else {
		handler = server.NewHandler(adapter, nil)
	}

	srv := rest.MustNewServer(rest.RestConf{Host: host, Port: port})
	defer srv.Stop()

	for _, route := range apiRoutes(handler) {
		srv.AddRoute(route)
	}
	srv.Start()
}

func ensurePostgresSchema(ctx context.Context, pool *pgxpool.Pool, cfg postgres.Config) error {
	statements, err := postgres.SchemaSQL(cfg)
	if err != nil {
		return err
	}
	for _, stmt := range statements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
