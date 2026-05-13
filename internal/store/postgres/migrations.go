package postgres

import (
	"bytes"
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
	"text/template"
)

//go:embed sql/migrations/*.sql
var migrationFS embed.FS

type migrationData struct {
	SchemaName          string
	NodesTable          string
	ContentTable        string
	RepoNodesTable      string
	DocsTable           string
	RepoPathsTable      string
	CollectionsTable    string
	CollectionDocsTable string
	PathColumn          string
	KindColumn          string
	SizeColumn          string
	MTimeColumn         string
}

func SchemaSQL(cfg Config) ([]string, error) {
	data, err := migrationTemplateData(cfg)
	if err != nil {
		return nil, err
	}

	entries, err := migrationFS.ReadDir("sql/migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	statements := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if cfg.Schema == "" && name == "001_create_schema.sql" {
			continue
		}
		raw, err := migrationFS.ReadFile(path.Join("sql/migrations", name))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		statement, err := renderMigration(name, string(raw), data)
		if err != nil {
			return nil, err
		}
		if statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements, nil
}

func migrationTemplateData(cfg Config) (migrationData, error) {
	nodesTable, err := quoteTable(cfg.Schema, cfg.NodesTable)
	if err != nil {
		return migrationData{}, err
	}
	contentTable, err := quoteTable(cfg.Schema, cfg.ContentTable)
	if err != nil {
		return migrationData{}, err
	}
	repoNodesTable, err := quoteTable(cfg.Schema, cfg.RepoNodesTable)
	if err != nil {
		return migrationData{}, err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return migrationData{}, fmt.Errorf("path column: %w", err)
	}
	kindCol, err := quoteIdent(cfg.Files.KindColumn)
	if err != nil {
		return migrationData{}, fmt.Errorf("kind column: %w", err)
	}
	sizeCol, err := quoteIdent(cfg.Files.SizeColumn)
	if err != nil {
		return migrationData{}, fmt.Errorf("size column: %w", err)
	}
	mtimeCol, err := quoteIdent(cfg.Files.MTimeColumn)
	if err != nil {
		return migrationData{}, fmt.Errorf("mtime column: %w", err)
	}

	var schemaName string
	if cfg.Schema != "" {
		schemaName, err = quoteIdent(cfg.Schema)
		if err != nil {
			return migrationData{}, fmt.Errorf("schema: %w", err)
		}
	}

	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		return migrationData{}, err
	}
	repoPathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		return migrationData{}, err
	}
	collectionsTable, err := quoteTable(cfg.Schema, "gxfs_collections")
	if err != nil {
		return migrationData{}, err
	}
	collectionDocsTable, err := quoteTable(cfg.Schema, "gxfs_collection_docs")
	if err != nil {
		return migrationData{}, err
	}

	return migrationData{
		SchemaName:          schemaName,
		NodesTable:          nodesTable,
		ContentTable:        contentTable,
		RepoNodesTable:      repoNodesTable,
		DocsTable:           docsTable,
		RepoPathsTable:      repoPathsTable,
		CollectionsTable:    collectionsTable,
		CollectionDocsTable: collectionDocsTable,
		PathColumn:          pathCol,
		KindColumn:          kindCol,
		SizeColumn:          sizeCol,
		MTimeColumn:         mtimeCol,
	}, nil
}

func renderMigration(name, raw string, data migrationData) (string, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse migration %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render migration %s: %w", name, err)
	}
	return strings.TrimSpace(buf.String()), nil
}
