package store

import (
	"context"
	"fmt"
	"sort"
)

type Registry struct {
	repos map[string]Adapter
	docs  map[string]Adapter
}

var _ Adapter = (*Registry)(nil)
var _ CacheInvalidator = (*Registry)(nil)
var _ MountSourceLister = (*Registry)(nil)
var _ SourceRouter = (*Registry)(nil)

func NewRegistry(adapters map[string]Adapter) (*Registry, error) {
	return NewNamespaceRegistry(adapters, nil)
}

func NewNamespaceRegistry(repos map[string]Adapter, docs map[string]Adapter) (*Registry, error) {
	if len(repos) == 0 {
		return nil, fmt.Errorf("at least one repo is required")
	}
	repoAdapters, err := copyAdapters("repo", repos)
	if err != nil {
		return nil, err
	}
	docAdapters, err := copyAdapters("docs namespace", docs)
	if err != nil {
		return nil, err
	}
	return &Registry{repos: repoAdapters, docs: docAdapters}, nil
}

func copyAdapters(kind string, adapters map[string]Adapter) (map[string]Adapter, error) {
	cp := make(map[string]Adapter, len(adapters))
	for name, adapter := range adapters {
		if name == "" {
			return nil, fmt.Errorf("%s name is required", kind)
		}
		if adapter == nil {
			return nil, fmt.Errorf("adapter for %s %q is nil", kind, name)
		}
		cp[name] = adapter
	}
	return cp, nil
}

func (r *Registry) Repos() []string {
	repos := make([]string, 0, len(r.repos))
	for repo := range r.repos {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

func (r *Registry) MountSources(context.Context) ([]MountSource, error) {
	sources := make([]MountSource, 0, len(r.repos)+len(r.docs))
	for repo := range r.repos {
		ref := SourceRef{Kind: SourceKindRepo, Name: repo}.String()
		sources = append(sources, MountSource{
			Ref:         ref,
			Kind:        SourceKindRepo,
			Name:        repo,
			Description: "repository namespace",
		})
	}
	for name := range r.docs {
		ref := SourceRef{Kind: SourceKindDocs, Name: name}.String()
		sources = append(sources, MountSource{
			Ref:         ref,
			Kind:        SourceKindDocs,
			Name:        name,
			Description: "shared docs namespace",
		})
	}
	sort.SliceStable(sources, func(i, j int) bool {
		return sources[i].Ref < sources[j].Ref
	})
	return sources, nil
}

func (r *Registry) AdapterForSource(_ context.Context, source SourceRef) (Adapter, error) {
	switch source.Kind {
	case SourceKindRepo:
		return r.adapter(source.Name)
	case SourceKindDocs:
		adapter, ok := r.docs[source.Name]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownSource, source.String())
		}
		return adapter, nil
	case SourceKindDocset:
		return nil, fmt.Errorf("%w: %s", ErrNotSupported, source.Kind)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownSource, source.String())
	}
}

func (r *Registry) adapter(repo string) (Adapter, error) {
	adapter, ok := r.repos[repo]
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

func (r *Registry) Locate(ctx context.Context, req LocateRequest) (*LocateResponse, error) {
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Locate(ctx, req)
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
	for _, adapters := range []map[string]Adapter{r.repos, r.docs} {
		for _, adapter := range adapters {
			if invalidator, ok := adapter.(CacheInvalidator); ok {
				invalidator.Invalidate()
			}
		}
	}
}
