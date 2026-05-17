//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"testing"

	"gxfs/internal/store"
	"gxfs/internal/store/postgres"
)

// TestCollectionCRUD tests basic collection create/read/delete operations.
func TestCollectionCRUD(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-collection-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:        dsn,
		Schema:     "public",
		Repo:       "test-repo",
		RepoTable:  "vfs_repo_nodes",
		NodesTable: "vfs_nodes",
	}

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)

	// Create collection adapter
	collectionMgr := postgres.NewCollectionAdapter(pool, "public")

	// Test 1: Create collection
	t.Run("Create", func(t *testing.T) {
		resp, err := collectionMgr.CreateCollection(ctx, store.CreateCollectionRequest{
			Name:        "test-collection",
			Description: "A test collection",
		})
		if err != nil {
			t.Fatalf("CreateCollection: %v", err)
		}
		if resp.Collection.Name != "test-collection" {
			t.Errorf("name = %q, want test-collection", resp.Collection.Name)
		}
		if resp.Collection.ID == "" {
			t.Error("ID is empty")
		}
	})

	// Test 2: Invalid name (uppercase)
	t.Run("InvalidName_Uppercase", func(t *testing.T) {
		_, err := collectionMgr.CreateCollection(ctx, store.CreateCollectionRequest{
			Name: "Invalid-Name",
		})
		if err != store.ErrInvalidName {
			t.Errorf("error = %v, want ErrInvalidName", err)
		}
	})

	// Test 3: Duplicate name
	t.Run("DuplicateName", func(t *testing.T) {
		_, err := collectionMgr.CreateCollection(ctx, store.CreateCollectionRequest{
			Name: "test-collection",
		})
		if err != store.ErrNameExists {
			t.Errorf("error = %v, want ErrNameExists", err)
		}
	})

	// Test 4: List collections
	t.Run("List", func(t *testing.T) {
		resp, err := collectionMgr.ListCollections(ctx)
		if err != nil {
			t.Fatalf("ListCollections: %v", err)
		}
		if len(resp.Collections) != 1 {
			t.Errorf("got %d collections, want 1", len(resp.Collections))
		}
	})

	// Test 5: Get collection
	t.Run("Get", func(t *testing.T) {
		resp, err := collectionMgr.GetCollection(ctx, "test-collection")
		if err != nil {
			t.Fatalf("GetCollection: %v", err)
		}
		if resp.Collection.Name != "test-collection" {
			t.Errorf("name = %q, want test-collection", resp.Collection.Name)
		}
		if len(resp.Members) != 0 {
			t.Errorf("got %d members, want 0", len(resp.Members))
		}
	})

	// Test 6: Get non-existent collection
	t.Run("GetNotFound", func(t *testing.T) {
		_, err := collectionMgr.GetCollection(ctx, "non-existent")
		if err != store.ErrCollectionNotFound {
			t.Errorf("error = %v, want ErrCollectionNotFound", err)
		}
	})

	// Test 7: Delete collection
	t.Run("Delete", func(t *testing.T) {
		err := collectionMgr.DeleteCollection(ctx, "test-collection")
		if err != nil {
			t.Fatalf("DeleteCollection: %v", err)
		}

		// Verify deleted
		_, err = collectionMgr.GetCollection(ctx, "test-collection")
		if err != store.ErrCollectionNotFound {
			t.Errorf("after delete, error = %v, want ErrCollectionNotFound", err)
		}
	})
}

// TestCollectionMembership tests add/remove member operations.
func TestCollectionMembership(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-collection-member-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:        dsn,
		Schema:     "public",
		Repo:       "test-repo",
		RepoTable:  "vfs_repo_nodes",
		NodesTable: "vfs_nodes",
	}

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)
	seedBackfillData(t, containerName)

	// Run backfill to populate gxfs_docs and gxfs_repo_paths
	if _, err := postgres.BackfillDocs(ctx, pool, cfg); err != nil {
		t.Fatalf("BackfillDocs: %v", err)
	}

	collectionMgr := postgres.NewCollectionAdapter(pool, "public")

	// Create collection
	_, err := collectionMgr.CreateCollection(ctx, store.CreateCollectionRequest{
		Name:        "member-test",
		Description: "Test collection for membership",
	})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	// Test 1: Add member
	t.Run("AddMember", func(t *testing.T) {
		resp, err := collectionMgr.AddMember(ctx, store.AddMemberRequest{
			Name:      "member-test",
			SourceRef: "repo://test-repo/README.md",
			Path:      "/readme.md",
		})
		if err != nil {
			t.Fatalf("AddMember: %v", err)
		}
		if resp.Member.Path != "/readme.md" {
			t.Errorf("path = %q, want /readme.md", resp.Member.Path)
		}
		if resp.Member.DocID == "" {
			t.Error("DocID is empty")
		}
	})

	// Test 2: Add member with duplicate path
	t.Run("AddMember_DuplicatePath", func(t *testing.T) {
		_, err := collectionMgr.AddMember(ctx, store.AddMemberRequest{
			Name:      "member-test",
			SourceRef: "repo://test-repo/docs/readme.md",
			Path:      "/readme.md", // Same path as before
		})
		if err != store.ErrMemberExists {
			t.Errorf("error = %v, want ErrMemberExists", err)
		}
	})

	// Test 3: Add member with duplicate doc
	t.Run("AddMember_DuplicateDoc", func(t *testing.T) {
		_, err := collectionMgr.AddMember(ctx, store.AddMemberRequest{
			Name:      "member-test",
			SourceRef: "repo://test-repo/README.md",
			Path:      "/readme-alias.md", // Same doc, different path
		})
		if err != store.ErrDocAlreadyInCollection {
			t.Errorf("error = %v, want ErrDocAlreadyInCollection", err)
		}
	})

	// Test 4: Reject non-repo:// source ref
	t.Run("AddMember_NonRepoRef", func(t *testing.T) {
		_, err := collectionMgr.AddMember(ctx, store.AddMemberRequest{
			Name:      "member-test",
			SourceRef: "collection://other/file.md",
			Path:      "/file.md",
		})
		if err == nil {
			t.Error("expected error for non-repo:// source ref")
		}
	})

	// Test 5: Get member content
	t.Run("GetMemberContent", func(t *testing.T) {
		resp, err := collectionMgr.GetMemberContent(ctx, store.GetMemberContentRequest{
			Name: "member-test",
			Path: "/readme.md",
		})
		if err != nil {
			t.Fatalf("GetMemberContent: %v", err)
		}
		if resp.Content == "" {
			t.Error("content is empty")
		}
		if resp.Hash == "" {
			t.Error("hash is empty")
		}
	})

	// Test 6: Remove member
	t.Run("RemoveMember", func(t *testing.T) {
		err := collectionMgr.RemoveMember(ctx, store.RemoveMemberRequest{
			Name: "member-test",
			Path: "/readme.md",
		})
		if err != nil {
			t.Fatalf("RemoveMember: %v", err)
		}

		// Verify removed
		resp, err := collectionMgr.GetCollection(ctx, "member-test")
		if err != nil {
			t.Fatalf("GetCollection: %v", err)
		}
		if len(resp.Members) != 0 {
			t.Errorf("got %d members, want 0", len(resp.Members))
		}
	})
}

// TestCollectionDeleteWithMembers tests deleting a collection that has members.
func TestCollectionDeleteWithMembers(t *testing.T) {
	requireDocker(t)

	pgPort := freePort(t)
	containerName := fmt.Sprintf("gxfs-collection-del-test-%d", pgPort)
	startPostgres(t, containerName, pgPort)

	dsn := fmt.Sprintf("postgres://gxfs:gxfs@127.0.0.1:%d/gxfs?sslmode=disable", pgPort)
	ctx := context.Background()

	cfg := postgres.Config{
		DSN:        dsn,
		Schema:     "public",
		Repo:       "test-repo",
		RepoTable:  "vfs_repo_nodes",
		NodesTable: "vfs_nodes",
	}

	pool := connectPool(t, ctx, dsn)
	defer pool.Close()

	applyMigrations(t, ctx, pool, cfg)
	seedBackfillData(t, containerName)

	if _, err := postgres.BackfillDocs(ctx, pool, cfg); err != nil {
		t.Fatalf("BackfillDocs: %v", err)
	}

	collectionMgr := postgres.NewCollectionAdapter(pool, "public")

	// Create collection and add members
	_, err := collectionMgr.CreateCollection(ctx, store.CreateCollectionRequest{
		Name: "delete-test",
	})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	_, err = collectionMgr.AddMember(ctx, store.AddMemberRequest{
		Name:      "delete-test",
		SourceRef: "repo://test-repo/README.md",
		Path:      "/readme.md",
	})
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	// Delete collection (should succeed, members deleted first)
	err = collectionMgr.DeleteCollection(ctx, "delete-test")
	if err != nil {
		t.Fatalf("DeleteCollection with members: %v", err)
	}

	// Verify collection is gone
	_, err = collectionMgr.GetCollection(ctx, "delete-test")
	if err != store.ErrCollectionNotFound {
		t.Errorf("after delete, error = %v, want ErrCollectionNotFound", err)
	}
}
