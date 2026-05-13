package store_test

import (
	"context"
	"testing"

	"gxfs/internal/store"
)

type fakeAdapter struct{}

func (fakeAdapter) LS(context.Context, store.LSRequest) (*store.LSResponse, error) {
	return &store.LSResponse{}, nil
}

func (fakeAdapter) Tree(context.Context, store.TreeRequest) (*store.TreeResponse, error) {
	return &store.TreeResponse{}, nil
}

func (fakeAdapter) Cat(context.Context, store.CatRequest) (*store.CatResponse, error) {
	return &store.CatResponse{}, nil
}

func (fakeAdapter) Grep(context.Context, store.GrepRequest) (*store.GrepResponse, error) {
	return &store.GrepResponse{}, nil
}

func (fakeAdapter) Find(context.Context, store.FindRequest) (*store.FindResponse, error) {
	return &store.FindResponse{}, nil
}

func (fakeAdapter) Stat(context.Context, store.StatRequest) (*store.StatResponse, error) {
	return &store.StatResponse{}, nil
}

func (fakeAdapter) Put(context.Context, store.PutRequest) (*store.PutResponse, error) {
	return &store.PutResponse{}, nil
}

func (fakeAdapter) Delete(context.Context, store.DeleteRequest) (*store.DeleteResponse, error) {
	return &store.DeleteResponse{}, nil
}

func (fakeAdapter) Edit(context.Context, store.EditRequest) (*store.EditResponse, error) {
	return nil, nil
}

func (fakeAdapter) BatchHashes(context.Context, store.HashRequest) (*store.HashResponse, error) {
	return &store.HashResponse{Hashes: []store.ContentHash{}}, nil
}

func (fakeAdapter) Search(_ context.Context, _ store.SearchRequest) (*store.SearchResponse, error) {
	return &store.SearchResponse{Results: []store.SearchResult{}}, nil
}

var _ store.Adapter = fakeAdapter{}

func TestCapabilityRequestTypesCarryRepoAndPath(t *testing.T) {
	req := store.LSRequest{Repo: "gxfs", Path: "/docs"}
	if req.Repo != "gxfs" || req.Path != "/docs" {
		t.Fatalf("LSRequest = %+v, want repo and path preserved", req)
	}
}
