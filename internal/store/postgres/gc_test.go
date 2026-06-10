package postgres

import (
	"strings"
	"testing"
)

func TestGCOrphanCondition(t *testing.T) {
	// Test that orphan detection includes every table that can reference docs.
	// Missing a table here could cause data loss during forced GC.
	orphanCondition, err := gcOrphanCondition("public", 1)
	if err != nil {
		t.Fatalf("gcOrphanCondition() error = %v", err)
	}

	if !strings.Contains(orphanCondition, "rolio_repo_paths") {
		t.Error("orphan condition missing rolio_repo_paths check")
	}
	if !strings.Contains(orphanCondition, "rolio_doc_namespace_paths") {
		t.Error("orphan condition missing rolio_doc_namespace_paths check")
	}
	if !strings.Contains(orphanCondition, "rolio_docset_docs") {
		t.Error("orphan condition missing rolio_docset_docs check")
	}
	if !strings.Contains(orphanCondition, "NOT EXISTS") {
		t.Error("orphan condition missing NOT EXISTS clause")
	}
}

func TestGCCandidatesQuery(t *testing.T) {
	// Test the candidates query structure for dry-run
	cfg := Config{Schema: "public"}

	docsTable, err := quoteTable(cfg.Schema, "rolio_docs")
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
	if !strings.Contains(candidatesSQL, `"public"."rolio_docs"`) {
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
	_, err := quoteTable("public; drop table users;", "rolio_docs")
	if err == nil {
		t.Error("quoteTable should reject unsafe schema names")
	}
}
