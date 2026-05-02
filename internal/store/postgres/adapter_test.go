package postgres

import (
	"testing"

	"gxfs/internal/store"
)

var _ store.Adapter = (*Adapter)(nil)

func TestListFilesSQLQuotesIdentifiers(t *testing.T) {
	sql, err := ListFilesSQL(Config{
		Schema: "public",
		Files: FileTableConfig{
			Table:         "vfs_files",
			PathColumn:    "path",
			ContentColumn: "content",
			SizeColumn:    "size",
			MTimeColumn:   "updated_at",
		},
	})
	if err != nil {
		t.Fatalf("ListFilesSQL() error = %v", err)
	}

	want := `select "path", "content", "size", "updated_at" from "public"."vfs_files" order by "path"`
	if sql != want {
		t.Fatalf("ListFilesSQL() = %q, want %q", sql, want)
	}
}

func TestListFilesSQLRejectsUnsafeIdentifier(t *testing.T) {
	_, err := ListFilesSQL(Config{
		Schema: "public",
		Files: FileTableConfig{
			Table:         `vfs_files; drop table users;`,
			PathColumn:    "path",
			ContentColumn: "content",
		},
	})
	if err == nil {
		t.Fatal("ListFilesSQL() error = nil, want unsafe identifier rejection")
	}
}
