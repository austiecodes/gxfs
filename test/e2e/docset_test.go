//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/austiecodes/rolio/internal/store"
	"github.com/austiecodes/rolio/internal/store/postgres"
)

// TestDocsetCRUD tests basic docset create/read/delete operations.
func TestDocsetCRUD(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("rolio-docset-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://rolio:rolio@127.0.0.1:%d/rolio?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := e2ePostgresConfig(dsn, "test-repo")

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)

	docsetMgr := postgres.NewDocsetAdapter(pool, "public")

	// Test 1: Create docset
	t.Run("Create", func(t *testing.T) {
		resp, err := docsetMgr.CreateDocset(ctx, store.CreateDocsetRequest{
			Name:        "test-docset",
			Description: "A test docset",
		})
		if err != nil {
			t.Fatalf("CreateDocset: %v", err)
		}
		if resp.Docset.Name != "test-docset" {
			t.Errorf("name = %q, want test-docset", resp.Docset.Name)
		}
		if resp.Docset.ID == "" {
			t.Error("ID is empty")
		}
	})

	// Test 2: Invalid name (uppercase)
	t.Run("InvalidName_Uppercase", func(t *testing.T) {
		_, err := docsetMgr.CreateDocset(ctx, store.CreateDocsetRequest{
			Name: "Invalid-Name",
		})
		if err != store.ErrInvalidName {
			t.Errorf("error = %v, want ErrInvalidName", err)
		}
	})

	// Test 3: Duplicate name
	t.Run("DuplicateName", func(t *testing.T) {
		_, err := docsetMgr.CreateDocset(ctx, store.CreateDocsetRequest{
			Name: "test-docset",
		})
		if err != store.ErrDocsetNameExists {
			t.Errorf("error = %v, want ErrDocsetNameExists", err)
		}
	})

	// Test 4: List docsets
	t.Run("List", func(t *testing.T) {
		resp, err := docsetMgr.ListDocsets(ctx)
		if err != nil {
			t.Fatalf("ListDocsets: %v", err)
		}
		if len(resp.Docsets) != 1 {
			t.Errorf("got %d docsets, want 1", len(resp.Docsets))
		}
	})

	// Test 5: Get docset
	t.Run("Get", func(t *testing.T) {
		resp, err := docsetMgr.GetDocset(ctx, "test-docset")
		if err != nil {
			t.Fatalf("GetDocset: %v", err)
		}
		if resp.Docset.Name != "test-docset" {
			t.Errorf("name = %q, want test-docset", resp.Docset.Name)
		}
		if len(resp.Members) != 0 {
			t.Errorf("got %d members, want 0", len(resp.Members))
		}
	})

	// Test 6: Get non-existent docset
	t.Run("GetNotFound", func(t *testing.T) {
		_, err := docsetMgr.GetDocset(ctx, "non-existent")
		if err != store.ErrDocsetNotFound {
			t.Errorf("error = %v, want ErrDocsetNotFound", err)
		}
	})

	// Test 7: Delete docset
	t.Run("Delete", func(t *testing.T) {
		err := docsetMgr.DeleteDocset(ctx, "test-docset")
		if err != nil {
			t.Fatalf("DeleteDocset: %v", err)
		}

		// Verify deleted
		_, err = docsetMgr.GetDocset(ctx, "test-docset")
		if err != store.ErrDocsetNotFound {
			t.Errorf("after delete, error = %v, want ErrDocsetNotFound", err)
		}
	})
}

// TestDocsetMembership tests add/remove member operations.
func TestDocsetMembership(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("rolio-docset-member-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://rolio:rolio@127.0.0.1:%d/rolio?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := e2ePostgresConfig(dsn, "test-repo")

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)
	seedBackfillData(t, containerName)

	// Run backfill to populate rolio_docs and rolio_repo_paths
	if _, err := postgres.BackfillDocs(ctx, pool, cfg); err != nil {
		t.Fatalf("BackfillDocs: %v", err)
	}

	docsetMgr := postgres.NewDocsetAdapter(pool, "public")

	// Create docset
	_, err := docsetMgr.CreateDocset(ctx, store.CreateDocsetRequest{
		Name:        "member-test",
		Description: "Test docset for membership",
	})
	if err != nil {
		t.Fatalf("CreateDocset: %v", err)
	}

	// Test 1: Add member
	t.Run("AddDocsetMember", func(t *testing.T) {
		resp, err := docsetMgr.AddDocsetMember(ctx, store.AddDocsetMemberRequest{
			Name:      "member-test",
			SourceRef: "repo://test-repo/README.md",
			Path:      "/readme.md",
		})
		if err != nil {
			t.Fatalf("AddDocsetMember: %v", err)
		}
		if resp.Member.Path != "/readme.md" {
			t.Errorf("path = %q, want /readme.md", resp.Member.Path)
		}
		if resp.Member.DocID == "" {
			t.Error("DocID is empty")
		}
	})

	// Test 2: Add member with duplicate path
	t.Run("AddDocsetMember_DuplicatePath", func(t *testing.T) {
		_, err := docsetMgr.AddDocsetMember(ctx, store.AddDocsetMemberRequest{
			Name:      "member-test",
			SourceRef: "repo://test-repo/docs/readme.md",
			Path:      "/readme.md", // Same path as before
		})
		if err != store.ErrDocsetMemberExists {
			t.Errorf("error = %v, want ErrDocsetMemberExists", err)
		}
	})

	// Test 3: Add member with duplicate doc
	t.Run("AddDocsetMember_DuplicateDoc", func(t *testing.T) {
		_, err := docsetMgr.AddDocsetMember(ctx, store.AddDocsetMemberRequest{
			Name:      "member-test",
			SourceRef: "repo://test-repo/README.md",
			Path:      "/readme-alias.md", // Same doc, different path
		})
		if err != store.ErrDocAlreadyInDocset {
			t.Errorf("error = %v, want ErrDocAlreadyInDocset", err)
		}
	})

	// Test 4: Reject non-repo:// source ref
	t.Run("AddDocsetMember_NonRepoRef", func(t *testing.T) {
		_, err := docsetMgr.AddDocsetMember(ctx, store.AddDocsetMemberRequest{
			Name:      "member-test",
			SourceRef: "docset://other/file.md",
			Path:      "/file.md",
		})
		if err == nil {
			t.Error("expected error for non-repo:// source ref")
		}
	})

	// Test 5: Get member content
	t.Run("GetDocsetMemberContent", func(t *testing.T) {
		resp, err := docsetMgr.GetDocsetMemberContent(ctx, store.GetDocsetMemberContentRequest{
			Name: "member-test",
			Path: "/readme.md",
		})
		if err != nil {
			t.Fatalf("GetDocsetMemberContent: %v", err)
		}
		if resp.Content == "" {
			t.Error("content is empty")
		}
		if resp.Hash == "" {
			t.Error("hash is empty")
		}
	})

	// Test 6: Remove member
	t.Run("RemoveDocsetMember", func(t *testing.T) {
		err := docsetMgr.RemoveDocsetMember(ctx, store.RemoveDocsetMemberRequest{
			Name: "member-test",
			Path: "/readme.md",
		})
		if err != nil {
			t.Fatalf("RemoveDocsetMember: %v", err)
		}

		// Verify removed
		resp, err := docsetMgr.GetDocset(ctx, "member-test")
		if err != nil {
			t.Fatalf("GetDocset: %v", err)
		}
		if len(resp.Members) != 0 {
			t.Errorf("got %d members, want 0", len(resp.Members))
		}
	})
}

// TestDocsetDeleteWithMembers tests deleting a docset that has members.
func TestDocsetDeleteWithMembers(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("rolio-docset-del-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://rolio:rolio@127.0.0.1:%d/rolio?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := e2ePostgresConfig(dsn, "test-repo")

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)
	seedBackfillData(t, containerName)

	if _, err := postgres.BackfillDocs(ctx, pool, cfg); err != nil {
		t.Fatalf("BackfillDocs: %v", err)
	}

	docsetMgr := postgres.NewDocsetAdapter(pool, "public")

	// Create docset and add members
	_, err := docsetMgr.CreateDocset(ctx, store.CreateDocsetRequest{
		Name: "delete-test",
	})
	if err != nil {
		t.Fatalf("CreateDocset: %v", err)
	}

	_, err = docsetMgr.AddDocsetMember(ctx, store.AddDocsetMemberRequest{
		Name:      "delete-test",
		SourceRef: "repo://test-repo/README.md",
		Path:      "/readme.md",
	})
	if err != nil {
		t.Fatalf("AddDocsetMember: %v", err)
	}

	// Delete docset (should succeed, members deleted first)
	err = docsetMgr.DeleteDocset(ctx, "delete-test")
	if err != nil {
		t.Fatalf("DeleteDocset with members: %v", err)
	}

	// Verify docset is gone
	_, err = docsetMgr.GetDocset(ctx, "delete-test")
	if err != store.ErrDocsetNotFound {
		t.Errorf("after delete, error = %v, want ErrDocsetNotFound", err)
	}
}
