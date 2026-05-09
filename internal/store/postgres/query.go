package postgres

import (
	"fmt"
	"regexp"
)

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ListNodesSQL(cfg Config) (string, error) {
	nodesTable, err := quoteTable(cfg.Schema, cfg.NodesTable)
	if err != nil {
		return "", err
	}
	mappingTable, err := quoteTable(cfg.Schema, cfg.RepoNodesTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	kindCol, err := quoteIdent(cfg.Files.KindColumn)
	if err != nil {
		return "", fmt.Errorf("kind column: %w", err)
	}

	sizeExpr := "0"
	if cfg.Files.SizeColumn != "" {
		sizeCol, err := quoteIdent(cfg.Files.SizeColumn)
		if err != nil {
			return "", fmt.Errorf("size column: %w", err)
		}
		sizeExpr = "n." + sizeCol
	}

	mtimeExpr := "null::timestamptz"
	if cfg.Files.MTimeColumn != "" {
		mtimeCol, err := quoteIdent(cfg.Files.MTimeColumn)
		if err != nil {
			return "", fmt.Errorf("mtime column: %w", err)
		}
		mtimeExpr = "n." + mtimeCol
	}

	return fmt.Sprintf(
		"select n.%s, n.%s, %s, %s from %s n join %s r on n.%s = r.%s where r.repo = $1 order by n.%s",
		pathCol, kindCol, sizeExpr, mtimeExpr,
		nodesTable, mappingTable, pathCol, pathCol, pathCol,
	), nil
}

func LoadContentSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, cfg.ContentTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	return fmt.Sprintf("select content from %s where %s = $1", table, pathCol), nil
}

func LoadContentUnderSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, cfg.ContentTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	return fmt.Sprintf("select %s, content from %s where %s like $1 order by %s", pathCol, table, pathCol, pathCol), nil
}

func UpsertNodeSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, cfg.NodesTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", err
	}
	kindCol, err := quoteIdent(cfg.Files.KindColumn)
	if err != nil {
		return "", err
	}
	sizeCol, err := quoteIdent(cfg.Files.SizeColumn)
	if err != nil {
		return "", err
	}
	mtimeCol, err := quoteIdent(cfg.Files.MTimeColumn)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`insert into %s(%s, %s, %s, %s) values($1, $2, $3, now()) on conflict(%s) do update set %s = excluded.%s, %s = excluded.%s, %s = now()`,
		table, pathCol, kindCol, sizeCol, mtimeCol,
		pathCol, kindCol, kindCol, sizeCol, sizeCol, mtimeCol,
	), nil
}

func UpsertContentSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, cfg.ContentTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	return fmt.Sprintf("insert into %s(%s, content) values($1, $2) on conflict(%s) do update set content = excluded.content", table, pathCol, pathCol), nil
}

func UpsertRepoNodeSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, cfg.RepoNodesTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	return fmt.Sprintf("insert into %s(repo, %s) values($1, $2) on conflict do nothing", table, pathCol), nil
}

func DeleteRepoNodeSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, cfg.RepoNodesTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	return fmt.Sprintf("delete from %s where repo = $1 and %s = $2", table, pathCol), nil
}

func CleanOrphanNodeSQL(cfg Config) (string, error) {
	nodesTable, err := quoteTable(cfg.Schema, cfg.NodesTable)
	if err != nil {
		return "", err
	}
	mappingTable, err := quoteTable(cfg.Schema, cfg.RepoNodesTable)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	return fmt.Sprintf(
		"delete from %s where %s = $1 and not exists (select 1 from %s where %s = $1)",
		nodesTable, pathCol, mappingTable, pathCol,
	), nil
}

func quoteTable(schema, table string) (string, error) {
	tableName, err := quoteIdent(table)
	if err != nil {
		return "", fmt.Errorf("table: %w", err)
	}
	if schema == "" {
		return tableName, nil
	}
	schemaName, err := quoteIdent(schema)
	if err != nil {
		return "", fmt.Errorf("schema: %w", err)
	}
	return schemaName + "." + tableName, nil
}

func quoteIdent(s string) (string, error) {
	if !identPattern.MatchString(s) {
		return "", fmt.Errorf("unsafe identifier %q", s)
	}
	return `"` + s + `"`, nil
}
