package server

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/austiecodes/gxfs/internal/store"
)

type repoCatalog interface {
	ListRepos(context.Context) ([]store.RepoInfo, error)
	RegisterRepo(context.Context, store.RegisterRepoRequest) (*store.RegisterRepoResponse, error)
}

type namespaceCatalog interface {
	ListDocNamespaces(context.Context) ([]store.DocNamespace, error)
	ListDocsets(context.Context) ([]store.Docset, error)
}

type SourceAdapterFactory func(context.Context, DynamicSource) (store.Adapter, error)

// DynamicSource describes a DB-registered source namespace that needs an
// adapter in the in-process routing table.
type DynamicSource struct {
	Kind        store.SourceKind
	Name        string
	Writable    bool
	Description string
}

type dynamicSourceEntry struct {
	source  DynamicSource
	adapter store.Adapter
}

// DynamicRegistry routes requests through a registry that is periodically
// rebuilt from the durable DB catalog.
type DynamicRegistry struct {
	catalog    repoCatalog
	namespaces namespaceCatalog
	factory    SourceAdapterFactory

	mu      sync.RWMutex
	repos   map[string]dynamicSourceEntry
	docs    map[string]dynamicSourceEntry
	docsets []store.Docset
}

var _ store.Adapter = (*DynamicRegistry)(nil)
var _ store.CacheInvalidator = (*DynamicRegistry)(nil)
var _ store.MountSourceLister = (*DynamicRegistry)(nil)
var _ store.SourceRouter = (*DynamicRegistry)(nil)
var _ store.RepoRegistry = (*DynamicRegistry)(nil)
var _ store.UsageRecorder = (*DynamicRegistry)(nil)

func NewDynamicRegistry(ctx context.Context, catalog repoCatalog, namespaces namespaceCatalog, factory SourceAdapterFactory) (*DynamicRegistry, error) {
	if catalog == nil {
		return nil, fmt.Errorf("repo catalog is required")
	}
	if factory == nil {
		return nil, fmt.Errorf("source adapter factory is required")
	}
	r := &DynamicRegistry{
		catalog:    catalog,
		namespaces: namespaces,
		factory:    factory,
		repos:      map[string]dynamicSourceEntry{},
		docs:       map[string]dynamicSourceEntry{},
	}
	if err := r.Refresh(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *DynamicRegistry) StartRefreshLoop(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 {
		close(done)
		return done
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.Refresh(ctx); err != nil {
					slog.Warn("refresh registry", "error", err)
				}
			}
		}
	}()
	return done
}

func (r *DynamicRegistry) Refresh(ctx context.Context) error {
	repos, err := r.catalog.ListRepos(ctx)
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}

	var docs []store.DocNamespace
	var docsets []store.Docset
	if r.namespaces != nil {
		docs, err = r.namespaces.ListDocNamespaces(ctx)
		if err != nil {
			return fmt.Errorf("list docs namespaces: %w", err)
		}
		docsets, err = r.namespaces.ListDocsets(ctx)
		if err != nil {
			return fmt.Errorf("list docsets: %w", err)
		}
	}

	r.mu.RLock()
	currentRepos := r.repos
	currentDocs := r.docs
	r.mu.RUnlock()

	nextRepos := make(map[string]dynamicSourceEntry, len(repos))
	for _, repo := range repos {
		if repo.Name == "" {
			continue
		}
		source := DynamicSource{Kind: store.SourceKindRepo, Name: repo.Name, Writable: repo.Writable}
		entry, ok := currentRepos[repo.Name]
		if !ok {
			adapter, err := r.factory(ctx, source)
			if err != nil {
				return fmt.Errorf("create repo adapter %q: %w", repo.Name, err)
			}
			entry = dynamicSourceEntry{adapter: adapter}
		}
		entry.source = source
		nextRepos[repo.Name] = entry
	}

	nextDocs := make(map[string]dynamicSourceEntry, len(docs))
	for _, namespace := range docs {
		if namespace.Name == "" {
			continue
		}
		source := DynamicSource{
			Kind:        store.SourceKindDocs,
			Name:        namespace.Name,
			Writable:    namespace.Writable,
			Description: namespace.Description,
		}
		entry, ok := currentDocs[namespace.Name]
		if !ok {
			adapter, err := r.factory(ctx, source)
			if err != nil {
				return fmt.Errorf("create docs adapter %q: %w", namespace.Name, err)
			}
			entry = dynamicSourceEntry{adapter: adapter}
		}
		entry.source = source
		nextDocs[namespace.Name] = entry
	}

	sort.SliceStable(docsets, func(i, j int) bool {
		return docsets[i].Name < docsets[j].Name
	})

	r.mu.Lock()
	r.repos = nextRepos
	r.docs = nextDocs
	r.docsets = docsets
	r.mu.Unlock()
	return nil
}

func (r *DynamicRegistry) ListRepos(ctx context.Context) ([]store.RepoInfo, error) {
	return r.catalog.ListRepos(ctx)
}

func (r *DynamicRegistry) RegisterRepo(ctx context.Context, req store.RegisterRepoRequest) (*store.RegisterRepoResponse, error) {
	resp, err := r.catalog.RegisterRepo(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := r.Refresh(ctx); err != nil {
		return nil, fmt.Errorf("refresh registry after register repo: %w", err)
	}
	return resp, nil
}

func (r *DynamicRegistry) RecordUsageEvent(ctx context.Context, event store.UsageEvent) (*store.UsageEventResponse, error) {
	recorder, ok := r.catalog.(store.UsageRecorder)
	if !ok {
		return nil, fmt.Errorf("%w: usage event recording is not supported", store.ErrNotSupported)
	}
	return recorder.RecordUsageEvent(ctx, event)
}

func (r *DynamicRegistry) Repos() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	repos := make([]string, 0, len(r.repos))
	for repo := range r.repos {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

func (r *DynamicRegistry) RepoWritable(repo string) bool {
	writable, ok := r.lookupRepoWritable(repo)
	return ok && writable
}

func (r *DynamicRegistry) RepoWritableWithRefresh(ctx context.Context, repo string) (bool, error) {
	if writable, ok := r.lookupRepoWritable(repo); ok {
		return writable, nil
	}
	if err := r.Refresh(ctx); err != nil {
		return false, err
	}
	writable, ok := r.lookupRepoWritable(repo)
	return ok && writable, nil
}

func (r *DynamicRegistry) lookupRepoWritable(repo string) (bool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.repos[repo]
	if !ok {
		return false, false
	}
	return entry.source.Writable, true
}

func (r *DynamicRegistry) MountSources(ctx context.Context) ([]store.MountSource, error) {
	if err := r.Refresh(ctx); err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	sources := make([]store.MountSource, 0, len(r.repos)+len(r.docs)+len(r.docsets))
	for _, entry := range r.repos {
		ref := store.SourceRef{Kind: store.SourceKindRepo, Name: entry.source.Name}.String()
		sources = append(sources, store.MountSource{
			Ref:         ref,
			Kind:        store.SourceKindRepo,
			Name:        entry.source.Name,
			Writable:    entry.source.Writable,
			Description: "repository namespace",
		})
	}
	for _, entry := range r.docs {
		ref := store.SourceRef{Kind: store.SourceKindDocs, Name: entry.source.Name}.String()
		description := entry.source.Description
		if description == "" {
			description = "shared docs namespace"
		}
		sources = append(sources, store.MountSource{
			Ref:         ref,
			Kind:        store.SourceKindDocs,
			Name:        entry.source.Name,
			Writable:    entry.source.Writable,
			Description: description,
		})
	}
	for _, docset := range r.docsets {
		ref := store.SourceRef{Kind: store.SourceKindDocset, Name: docset.Name}.String()
		description := docset.Description
		if description == "" {
			description = "curated docset"
		}
		sources = append(sources, store.MountSource{
			Ref:         ref,
			Kind:        store.SourceKindDocset,
			Name:        docset.Name,
			Description: description,
		})
	}
	sort.SliceStable(sources, func(i, j int) bool {
		return sources[i].Ref < sources[j].Ref
	})
	return sources, nil
}

func (r *DynamicRegistry) AdapterForSource(ctx context.Context, source store.SourceRef) (store.Adapter, error) {
	switch source.Kind {
	case store.SourceKindRepo:
		return r.repoAdapter(ctx, source.Name)
	case store.SourceKindDocs:
		return r.docsAdapter(ctx, source.Name)
	case store.SourceKindDocset:
		return nil, fmt.Errorf("%w: %s", store.ErrNotSupported, source.Kind)
	default:
		return nil, fmt.Errorf("%w: %s", store.ErrUnknownSource, source.String())
	}
}

func (r *DynamicRegistry) repoAdapter(ctx context.Context, repo string) (store.Adapter, error) {
	if adapter, ok := r.lookupRepo(repo); ok {
		return adapter, nil
	}
	if err := r.Refresh(ctx); err != nil {
		return nil, err
	}
	if adapter, ok := r.lookupRepo(repo); ok {
		return adapter, nil
	}
	return nil, fmt.Errorf("%w: %s", store.ErrUnknownRepo, repo)
}

func (r *DynamicRegistry) docsAdapter(ctx context.Context, name string) (store.Adapter, error) {
	if adapter, ok := r.lookupDocs(name); ok {
		return adapter, nil
	}
	if err := r.Refresh(ctx); err != nil {
		return nil, err
	}
	if adapter, ok := r.lookupDocs(name); ok {
		return adapter, nil
	}
	return nil, fmt.Errorf("%w: %s", store.ErrUnknownSource, store.SourceRef{Kind: store.SourceKindDocs, Name: name}.String())
}

func (r *DynamicRegistry) lookupRepo(repo string) (store.Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.repos[repo]
	return entry.adapter, ok
}

func (r *DynamicRegistry) lookupDocs(name string) (store.Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.docs[name]
	return entry.adapter, ok
}

func (r *DynamicRegistry) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.LS(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.LS(ctx, req)
}

func (r *DynamicRegistry) Tree(ctx context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Tree(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Tree(ctx, req)
}

func (r *DynamicRegistry) Cat(ctx context.Context, req store.CatRequest) (*store.CatResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Cat(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Cat(ctx, req)
}

func (r *DynamicRegistry) Grep(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Grep(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Grep(ctx, req)
}

func (r *DynamicRegistry) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Find(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Find(ctx, req)
}

func (r *DynamicRegistry) Stat(ctx context.Context, req store.StatRequest) (*store.StatResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Stat(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Stat(ctx, req)
}

func (r *DynamicRegistry) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Put(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Put(ctx, req)
}

func (r *DynamicRegistry) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Delete(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Delete(ctx, req)
}

func (r *DynamicRegistry) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Edit(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Edit(ctx, req)
}

func (r *DynamicRegistry) Search(ctx context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Search(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Search(ctx, req)
}

func (r *DynamicRegistry) Locate(ctx context.Context, req store.LocateRequest) (*store.LocateResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Locate(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Locate(ctx, req)
}

func (r *DynamicRegistry) BatchHashes(ctx context.Context, req store.HashRequest) (*store.HashResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.BatchHashes(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.BatchHashes(ctx, req)
}

func (r *DynamicRegistry) Glob(ctx context.Context, req store.GlobRequest) (*store.GlobResponse, error) {
	if req.Repo == "" {
		registry, err := r.storeRegistrySnapshot()
		if err != nil {
			return nil, err
		}
		return registry.Glob(ctx, req)
	}
	adapter, err := r.repoAdapter(ctx, req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Glob(ctx, req)
}

func (r *DynamicRegistry) Invalidate() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entries := range []map[string]dynamicSourceEntry{r.repos, r.docs} {
		for _, entry := range entries {
			if invalidator, ok := entry.adapter.(store.CacheInvalidator); ok {
				invalidator.Invalidate()
			}
		}
	}
}

func (r *DynamicRegistry) storeRegistrySnapshot() (*store.Registry, error) {
	r.mu.RLock()
	repos := make(map[string]store.Adapter, len(r.repos))
	for name, entry := range r.repos {
		repos[name] = entry.adapter
	}
	docs := make(map[string]store.Adapter, len(r.docs))
	for name, entry := range r.docs {
		docs[name] = entry.adapter
	}
	r.mu.RUnlock()
	return store.NewNamespaceRegistry(repos, docs)
}
