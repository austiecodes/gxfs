package postgres

import (
	"testing"

	"gxfs/internal/store"
)

var _ store.Adapter = (*Adapter)(nil)

func TestListNodesSQLJoinsRepoNodes(t *testing.T) {
	sql, err := ListNodesSQL(Config{
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
	})
	if err != nil {
		t.Fatalf("ListNodesSQL() error = %v", err)
	}

	want := `select n."path", n."kind", n."size", n."updated_at" from "public"."vfs_nodes" n join "public"."vfs_repo_nodes" r on n."path" = r."path" where r.repo = $1 order by n."path"`
	if sql != want {
		t.Fatalf("ListNodesSQL() = %q, want %q", sql, want)
	}
}

func TestListNodesSQLUsesNullableMTimeWhenUnconfigured(t *testing.T) {
	sql, err := ListNodesSQL(Config{
		Schema:         "public",
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn: "path",
			KindColumn: "kind",
			SizeColumn: "size",
		},
	})
	if err != nil {
		t.Fatalf("ListNodesSQL() error = %v", err)
	}

	want := `select n."path", n."kind", n."size", n.null::timestamptz from "public"."vfs_nodes" n join "public"."vfs_repo_nodes" r on n."path" = r."path" where r.repo = $1 order by n."path"`
	if sql != want {
		t.Fatalf("ListNodesSQL() = %q, want %q", sql, want)
	}
}

func TestListNodesSQLRejectsUnsafeIdentifier(t *testing.T) {
	_, err := ListNodesSQL(Config{
		Schema:         "public",
		NodesTable:     `vfs_nodes; drop table users;`,
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: FileTableConfig{
			PathColumn: "path",
			KindColumn: "kind",
		},
	})
	if err == nil {
		t.Fatal("ListNodesSQL() error = nil, want unsafe identifier rejection")
	}
}
