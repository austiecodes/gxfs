//go:build e2e

package e2e_test

import "github.com/austiecodes/rolio/internal/store/postgres"

func e2ePostgresConfig(dsn, repo string) postgres.Config {
	return postgres.Config{
		DSN:            dsn,
		Schema:         "public",
		Repo:           repo,
		NodesTable:     "vfs_nodes",
		ContentTable:   "vfs_content",
		RepoNodesTable: "vfs_repo_nodes",
		Files: postgres.FileTableConfig{
			PathColumn:  "path",
			KindColumn:  "kind",
			SizeColumn:  "size",
			MTimeColumn: "updated_at",
		},
	}
}
