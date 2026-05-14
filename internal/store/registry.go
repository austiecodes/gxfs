package store

import (
	"context"
	"fmt"
	"sort"
)

type Registry struct {
	adapters map[string]Adapter
}

var _ Adapter = (*Registry)(nil)
var _ CacheInvalidator = (*Registry)(nil)

func NewRegistry(adapters map[string]Adapter) (*Registry, error) {
	if len(adapters) == 0 {
		return nil, fmt.Errorf("at least one repo is required")
	}
	cp := make(map[string]Adapter, len(adapters))
	for repo, adapter := range adapters {
		if repo == "" {
			return nil, fmt.Errorf("repo name is required")
		}
		if adapter == nil {
			return nil, fmt.Errorf("adapter for repo %q is nil", repo)
		}
		cp[repo] = adapter
	}
	return &Registry{adapters: cp}, nil
}

func (r *Registry) Repos() []string {
	repos := make([]string, 0, len(r.adapters))
	for repo := range r.adapters {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

func (r *Registry) adapter(repo string) (Adapter, error) {
	adapter, ok := r.adapters[repo]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRepo, repo)
	}
	return adapter, nil
}

func (r *Registry) LS(ctx context.Context, req LSRequest) (*LSResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.LS(ctx, req)
}

func (r *Registry) Tree(ctx context.Context, req TreeRequest) (*TreeResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Tree(ctx, req)
}

func (r *Registry) Cat(ctx context.Context, req CatRequest) (*CatResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Cat(ctx, req)
}

func (r *Registry) Grep(ctx context.Context, req GrepRequest) (*GrepResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Grep(ctx, req)
}

func (r *Registry) Find(ctx context.Context, req FindRequest) (*FindResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Find(ctx, req)
}

func (r *Registry) Stat(ctx context.Context, req StatRequest) (*StatResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Stat(ctx, req)
}

func (r *Registry) Put(ctx context.Context, req PutRequest) (*PutResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Put(ctx, req)
}

func (r *Registry) Delete(ctx context.Context, req DeleteRequest) (*DeleteResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Delete(ctx, req)
}

func (r *Registry) Edit(ctx context.Context, req EditRequest) (*EditResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Edit(ctx, req)
}

func (r *Registry) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Search(ctx, req)
}

func (r *Registry) BatchHashes(ctx context.Context, req HashRequest) (*HashResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.BatchHashes(ctx, req)
}

func (r *Registry) Glob(ctx context.Context, req GlobRequest) (*GlobResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Glob(ctx, req)
}

func (r *Registry) Invalidate() {
	for _, adapter := range r.adapters {
		if invalidator, ok := adapter.(CacheInvalidator); ok {
			invalidator.Invalidate()
		}
	}
}
