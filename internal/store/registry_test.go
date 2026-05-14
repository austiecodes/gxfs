package store_test

import (
	"context"
	"errors"
	"testing"

	"gxfs/internal/store"
)

type registryFakeAdapter struct {
	lsReq       store.LSRequest
	invalidated bool
}

func (f *registryFakeAdapter) LS(_ context.Context, req store.LSRequest) (*store.LSResponse, error) {
	f.lsReq = req
	return &store.LSResponse{Nodes: []store.Node{{Path: req.Path, Name: "docs", Kind: "dir"}}}, nil
}

func (f *registryFakeAdapter) Tree(context.Context, store.TreeRequest) (*store.TreeResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Cat(context.Context, store.CatRequest) (*store.CatResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Grep(context.Context, store.GrepRequest) (*store.GrepResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Find(context.Context, store.FindRequest) (*store.FindResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Stat(context.Context, store.StatRequest) (*store.StatResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Put(context.Context, store.PutRequest) (*store.PutResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Delete(context.Context, store.DeleteRequest) (*store.DeleteResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Edit(context.Context, store.EditRequest) (*store.EditResponse, error) {
	return nil, nil
}

func (f *registryFakeAdapter) Invalidate() {
	f.invalidated = true
}

func (f *registryFakeAdapter) BatchHashes(_ context.Context, _ store.HashRequest) (*store.HashResponse, error) {
	return &store.HashResponse{Hashes: []store.ContentHash{}}, nil
}

func (f *registryFakeAdapter) Search(_ context.Context, _ store.SearchRequest) (*store.SearchResponse, error) {
	return &store.SearchResponse{Results: []store.SearchResult{}}, nil
}

func (f *registryFakeAdapter) Glob(_ context.Context, _ store.GlobRequest) (*store.GlobResponse, error) {
	return &store.GlobResponse{Results: []store.GlobResult{}}, nil
}

func TestRegistryRoutesByRepo(t *testing.T) {
	alpha := &registryFakeAdapter{}
	beta := &registryFakeAdapter{}
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"alpha": alpha,
		"beta":  beta,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	resp, err := registry.LS(context.Background(), store.LSRequest{Repo: "beta", Path: "/docs"})
	if err != nil {
		t.Fatalf("LS() error = %v", err)
	}
	if len(resp.Nodes) != 1 || beta.lsReq.Repo != "beta" || beta.lsReq.Path != "/docs" {
		t.Fatalf("beta LS req = %+v, resp = %+v", beta.lsReq, resp)
	}
	if alpha.lsReq.Repo != "" {
		t.Fatalf("alpha received request: %+v", alpha.lsReq)
	}
}

func TestRegistryRejectsUnknownRepo(t *testing.T) {
	registry, err := store.NewRegistry(map[string]store.Adapter{"alpha": &registryFakeAdapter{}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	_, err = registry.LS(context.Background(), store.LSRequest{Repo: "missing", Path: "/"})
	if !errors.Is(err, store.ErrUnknownRepo) {
		t.Fatalf("LS() error = %v, want ErrUnknownRepo", err)
	}
}

func TestRegistryInvalidatesAllAdapters(t *testing.T) {
	alpha := &registryFakeAdapter{}
	beta := &registryFakeAdapter{}
	registry, err := store.NewRegistry(map[string]store.Adapter{
		"alpha": alpha,
		"beta":  beta,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	registry.Invalidate()

	if !alpha.invalidated || !beta.invalidated {
		t.Fatalf("invalidated = %v/%v, want both true", alpha.invalidated, beta.invalidated)
	}
}
