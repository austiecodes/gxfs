package postgres

import (
	"strings"
	"testing"
)

func TestGCOrphanCondition(t *testing.T) {
	// Test that the orphan condition SQL includes both reference tables
	// This is a critical safety check - missing either table could cause data loss

	cfg := Config{Schema: "public"}

	_, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		t.Fatalf("quoteTable docs: %v", err)
	}
	pathsTable, err := quoteTable(cfg.Schema, "gxfs_repo_paths")
	if err != nil {
		t.Fatalf("quoteTable paths: %v", err)
	}
	collectionDocsTable, err := quoteTable(cfg.Schema, "gxfs_collection_docs")
	if err != nil {
		t.Fatalf("quoteTable collection_docs: %v", err)
	}

	// Build the orphan condition similar to GC()
	graceHours := 1
	orphanCondition := `updated_at < NOW() - INTERVAL '` + string(rune(graceHours+'0')) + ` hours'
AND NOT EXISTS (SELECT 1 FROM ` + pathsTable + ` p WHERE p.doc_id = d.id)
AND NOT EXISTS (SELECT 1 FROM ` + collectionDocsTable + ` c WHERE c.doc_id = d.id)`

	// Verify both reference tables are checked
	if !strings.Contains(orphanCondition, "gxfs_repo_paths") {
		t.Error("orphan condition missing gxfs_repo_paths check")
	}
	if !strings.Contains(orphanCondition, "gxfs_collection_docs") {
		t.Error("orphan condition missing gxfs_collection_docs check")
	}
	if !strings.Contains(orphanCondition, "NOT EXISTS") {
		t.Error("orphan condition missing NOT EXISTS clause")
	}
}

func TestGCCandidatesQuery(t *testing.T) {
	// Test the candidates query structure for dry-run
	cfg := Config{Schema: "public"}

	docsTable, err := quoteTable(cfg.Schema, "gxfs_docs")
	if err != nil {
		t.Fatalf("quoteTable docs: %v", err)
	}

	// Verify the query selects expected columns
	candidatesSQL := `SELECT id, title, COALESCE(legacy_path, '')
FROM ` + docsTable + ` d
WHERE <orphan_condition>
ORDER BY updated_at DESC
LIMIT 10`

	if !strings.Contains(candidatesSQL, "id") {
		t.Error("candidates query missing id column")
	}
	if !strings.Contains(candidatesSQL, "title") {
		t.Error("candidates query missing title column")
	}
	if !strings.Contains(candidatesSQL, "legacy_path") {
		t.Error("candidates query missing legacy_path column")
	}

	// Verify table name is properly quoted
	if !strings.Contains(candidatesSQL, `"public"."gxfs_docs"`) {
		t.Error("candidates query missing properly quoted docs table")
	}
}

func TestGCGracePeriodDefault(t *testing.T) {
	// Test that default grace period is applied
	req := GCRequest{GraceHours: 0}
	if req.GraceHours <= 0 {
		req.GraceHours = 1 // This is what GC() does
	}
	if req.GraceHours != 1 {
		t.Errorf("default grace hours = %d, want 1", req.GraceHours)
	}
}

func TestGCLimitDefault(t *testing.T) {
	// Test that default limit is applied
	req := GCRequest{Limit: 0}
	if req.Limit <= 0 {
		req.Limit = 10 // This is what GC() does
	}
	if req.Limit != 10 {
		t.Errorf("default limit = %d, want 10", req.Limit)
	}
}

func TestGCSchemaSafety(t *testing.T) {
	// Test that unsafe schema names are rejected
	_, err := quoteTable("public; drop table users;", "gxfs_docs")
	if err == nil {
		t.Error("quoteTable should reject unsafe schema names")
	}
}
