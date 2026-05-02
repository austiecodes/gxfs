package postgres

import (
	"fmt"
	"regexp"
)

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ListFilesSQL(cfg Config) (string, error) {
	table, err := quoteTable(cfg.Schema, cfg.Files.Table)
	if err != nil {
		return "", err
	}
	pathCol, err := quoteIdent(cfg.Files.PathColumn)
	if err != nil {
		return "", fmt.Errorf("path column: %w", err)
	}
	contentCol, err := quoteIdent(cfg.Files.ContentColumn)
	if err != nil {
		return "", fmt.Errorf("content column: %w", err)
	}

	sizeExpr := "0"
	if cfg.Files.SizeColumn != "" {
		sizeExpr, err = quoteIdent(cfg.Files.SizeColumn)
		if err != nil {
			return "", fmt.Errorf("size column: %w", err)
		}
	}

	mtimeExpr := "''"
	if cfg.Files.MTimeColumn != "" {
		mtimeExpr, err = quoteIdent(cfg.Files.MTimeColumn)
		if err != nil {
			return "", fmt.Errorf("mtime column: %w", err)
		}
	}

	return fmt.Sprintf("select %s, %s, %s, %s from %s order by %s",
		pathCol, contentCol, sizeExpr, mtimeExpr, table, pathCol), nil
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
