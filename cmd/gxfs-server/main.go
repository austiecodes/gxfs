package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
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

func adapterFromServerConfig(ctx context.Context, cfg config.ServerConfig) (store.Adapter, error) {
	repoNames := make(map[string]struct{}, len(cfg.Repos))
	for _, repo := range cfg.Repos {
		if _, exists := repoNames[repo.Name]; exists {
			return nil, fmt.Errorf("duplicate repo %q", repo.Name)
		}
		if err := validateRepoBackendType(repo.Backend.Type); err != nil {
			return nil, fmt.Errorf("repo %s: %w", repo.Name, err)
		}
		repoNames[repo.Name] = struct{}{}
	}

	docsNames := make(map[string]struct{}, len(cfg.Docs))
	for _, docs := range cfg.Docs {
		if _, exists := docsNames[docs.Name]; exists {
			return nil, fmt.Errorf("duplicate docs namespace %q", docs.Name)
		}
		if err := validateDocsNamespaceBackendType(docs.Backend.Type); err != nil {
			return nil, fmt.Errorf("docs namespace %s: %w", docs.Name, err)
		}
		docsNames[docs.Name] = struct{}{}
	}

	repoAdapters := make(map[string]store.Adapter, len(cfg.Repos))
	for _, repo := range cfg.Repos {
		adapter, err := adapterFromRepoConfig(ctx, repo)
		if err != nil {
			return nil, fmt.Errorf("repo %s: %w", repo.Name, err)
		}
		repoAdapters[repo.Name] = adapter
	}

	docsAdapters := make(map[string]store.Adapter, len(cfg.Docs))
	for _, docs := range cfg.Docs {
		adapter, err := adapterFromDocsNamespaceConfig(ctx, docs)
		if err != nil {
			return nil, fmt.Errorf("docs namespace %s: %w", docs.Name, err)
		}
		docsAdapters[docs.Name] = adapter
	}
	return store.NewNamespaceRegistry(repoAdapters, docsAdapters)
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
	return []rest.Route{
		{Method: http.MethodGet, Path: "/healthz", Handler: handler.ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/cache", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/repos", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/mount-sources", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/repos/:repo/:op", Handler: handler.ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/repos/:repo/:op", Handler: handler.ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/repos/:repo/:op", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/docs/:name/:op", Handler: handler.ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/docs/:name/:op", Handler: handler.ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/docs/:name/:op", Handler: handler.ServeHTTP},
		// Collection routes
		{Method: http.MethodPost, Path: "/v1/collections", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/collections", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/collections/:name", Handler: handler.ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/collections/:name", Handler: handler.ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/collections/:name/members", Handler: handler.ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/collections/:name/members", Handler: handler.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/collections/:name/docs", Handler: handler.ServeHTTP},
	}
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

// collectGCTargets extracts unique document storage targets from server config.
func collectGCTargets(cfg config.ServerConfig) []storageTarget {
	seen := make(map[storageTarget]bool)
	var targets []storageTarget
	for _, repo := range cfg.Repos {
		if repo.Backend.Type != "doc_postgres" {
			continue
		}
		targets = appendGCTarget(targets, seen, repo.Backend.Postgres)
	}
	for _, docs := range cfg.Docs {
		switch docs.Backend.Type {
		case "doc_postgres", "doc_namespace_postgres":
			targets = appendGCTarget(targets, seen, docs.Backend.Postgres)
		}
	}
	// Sort for deterministic output
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].dsn != targets[j].dsn {
			return targets[i].dsn < targets[j].dsn
		}
		return targets[i].schema < targets[j].schema
	})
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

	adapter, err := adapterFromServerConfig(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Build writable repos map from server config.
	writableRepos := make(map[string]bool, len(cfg.Repos))
	for _, repo := range cfg.Repos {
		if repo.Writable {
			writableRepos[repo.Name] = true
		}
	}

	// Create collection manager if we have a doc_postgres backend
	// V1 constraint: all doc_postgres backends must share the same (dsn, schema)
	// to ensure collection membership lookup works correctly across repos.
	var collectionMgr store.CollectionManager
	var collectionPool *pgxpool.Pool
	var collectionSchema string
	seenStorage := make(map[string]bool) // key: "dsn|schema"
	for _, repo := range cfg.Repos {
		if repo.Backend.Type == "doc_postgres" {
			pg := repo.Backend.Postgres
			storageKey := fmt.Sprintf("%s|%s", pg.DSN, pg.Schema)
			seenStorage[storageKey] = true
			if collectionPool == nil {
				pool, err := pgxpool.New(context.Background(), pg.DSN)
				if err != nil {
					fmt.Fprintln(os.Stderr, fmt.Errorf("connect collection db: %w", err))
					os.Exit(1)
				}
				collectionPool = pool
				collectionSchema = pg.Schema
			}
		}
	}
	if len(seenStorage) > 1 {
		fmt.Fprintln(os.Stderr, "error: V1 collections require all doc_postgres backends to share the same (dsn, schema). Found multiple distinct storage targets.")
		os.Exit(1)
	}
	if collectionPool != nil {
		collectionMgr = postgres.NewCollectionAdapter(collectionPool, collectionSchema)
	}

	var handler http.Handler
	if collectionMgr != nil {
		handler = server.NewHandlerWithCollections(adapter, writableRepos, collectionMgr)
	} else {
		handler = server.NewHandler(adapter, writableRepos)
	}

	srv := rest.MustNewServer(rest.RestConf{Host: host, Port: port})
	defer srv.Stop()

	for _, route := range apiRoutes(handler) {
		srv.AddRoute(route)
	}
	srv.Start()
}
