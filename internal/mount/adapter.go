package mount

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/austiecodes/gxfs/internal/store"
	"github.com/austiecodes/gxfs/internal/vfs"
)

type Adapter struct {
	base     store.Adapter
	resolver *Resolver
}

var _ store.Adapter = (*Adapter)(nil)

func NewAdapter(base store.Adapter, resolver *Resolver) *Adapter {
	return &Adapter{base: base, resolver: resolver}
}

func (a *Adapter) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	if tree, ok, err := a.compositeTreeIfNeeded(ctx, req.Path, req.All); err != nil {
		return nil, err
	} else if ok {
		nodes, err := tree.LS(displayLocal(req.Path), vfs.LSOptions{
			Sort:      req.Sort,
			Reverse:   req.Reverse,
			Recursive: req.Recursive,
			All:       req.All,
		})
		if err != nil {
			return nil, err
		}
		return &store.LSResponse{Nodes: paginateNodes(nodes, req.Limit, req.Offset), Total: len(nodes)}, nil
	}

	resolved, err := a.resolver.Resolve(req.Path, OpRead)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	resp, err := sourceAdapter.LS(ctx, req)
	if err != nil {
		return nil, err
	}
	a.localizeNodesSource(resolved.Source, resp.Nodes)
	return resp, nil
}

func (a *Adapter) Tree(ctx context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	if tree, ok, err := a.compositeTreeIfNeeded(ctx, req.Path, req.All); err != nil {
		return nil, err
	} else if ok {
		rootPath := displayLocal(req.Path)
		root, err := tree.Stat(rootPath)
		if err != nil {
			return nil, err
		}
		text, err := tree.Tree(rootPath, req.Depth, vfs.TreeOptions{
			All:       req.All,
			DirsOnly:  req.DirsOnly,
			FullPath:  req.FullPath,
			ShowSize:  req.ShowSize,
			Sort:      req.Sort,
			DirsFirst: req.DirsFirst,
		})
		if err != nil {
			return nil, err
		}
		return &store.TreeResponse{Root: root, Text: text}, nil
	}

	originalPath := req.Path
	resolved, err := a.resolver.Resolve(req.Path, OpRead)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	resp, err := sourceAdapter.Tree(ctx, req)
	if err != nil {
		return nil, err
	}
	if local, ok := a.resolver.ToLocalSource(sourceWithPath(resolved.Source, resp.Root.Path)); ok {
		resp.Root.Path = displayLocal(local)
		resp.Root.Name = pathBase(resp.Root.Path)
	}
	resp.Text = localizeTreeText(resp.Text, resolved.RemotePath, cleanLocal(originalPath), req.FullPath)
	return resp, nil
}

func (a *Adapter) Cat(ctx context.Context, req store.CatRequest) (*store.CatResponse, error) {
	resolved, err := a.resolver.Resolve(req.Path, OpRead)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	resp, err := sourceAdapter.Cat(ctx, req)
	if err != nil {
		return nil, err
	}
	if local, ok := a.resolver.ToLocalSource(sourceWithPath(resolved.Source, resp.Path)); ok {
		resp.Path = displayLocal(local)
	}
	return resp, nil
}

func (a *Adapter) Grep(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	if a.needsComposite(req.Path) {
		return a.grepComposite(ctx, req)
	}

	resolved, err := a.resolver.Resolve(req.Path, OpRead)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	resp, err := sourceAdapter.Grep(ctx, req)
	if err != nil {
		return nil, err
	}
	for i := range resp.Matches {
		if local, ok := a.resolver.ToLocalSource(sourceWithPath(resolved.Source, resp.Matches[i].Path)); ok {
			resp.Matches[i].Path = displayLocal(local)
		}
	}
	return resp, nil
}

func (a *Adapter) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	if tree, ok, err := a.compositeTreeIfNeeded(ctx, req.Path, req.All); err != nil {
		return nil, err
	} else if ok {
		nodes, err := tree.Find(displayLocal(req.Path), req.Name, vfs.FindOptions{
			Type:     req.Type,
			MaxDepth: req.MaxDepth,
			MinDepth: req.MinDepth,
			All:      req.All,
			IName:    req.IName,
		})
		if err != nil {
			return nil, err
		}
		return &store.FindResponse{Nodes: paginateNodes(nodes, req.Limit, req.Offset), Total: len(nodes)}, nil
	}

	resolved, err := a.resolver.Resolve(req.Path, OpRead)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	resp, err := sourceAdapter.Find(ctx, req)
	if err != nil {
		return nil, err
	}
	a.localizeNodesSource(resolved.Source, resp.Nodes)
	return resp, nil
}

func (a *Adapter) Stat(ctx context.Context, req store.StatRequest) (*store.StatResponse, error) {
	if a.resolver.hasVirtualDir(req.Path) {
		tree, err := a.buildCompositeTree(ctx, req.Path, true)
		if err != nil {
			return nil, err
		}
		node, err := tree.Stat(displayLocal(req.Path))
		if err != nil {
			return nil, err
		}
		return &store.StatResponse{Node: node}, nil
	}

	resolved, err := a.resolver.Resolve(req.Path, OpRead)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	resp, err := sourceAdapter.Stat(ctx, req)
	if err != nil {
		return nil, err
	}
	if local, ok := a.resolver.ToLocalSource(sourceWithPath(resolved.Source, resp.Node.Path)); ok {
		resp.Node.Path = displayLocal(local)
		resp.Node.Name = pathBase(resp.Node.Path)
	}
	return resp, nil
}

func (a *Adapter) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	resolved, err := a.resolver.Resolve(req.Path, OpWrite)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	resp, err := sourceAdapter.Put(ctx, req)
	if err != nil {
		return nil, err
	}
	if local, ok := a.resolver.ToLocalSource(sourceWithPath(resolved.Source, resp.Node.Path)); ok {
		resp.Node.Path = displayLocal(local)
		resp.Node.Name = pathBase(resp.Node.Path)
	}
	return resp, nil
}

func (a *Adapter) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	resolved, err := a.resolver.Resolve(req.Path, OpWrite)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	return sourceAdapter.Delete(ctx, req)
}

func (a *Adapter) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	resolved, err := a.resolver.Resolve(req.Path, OpWrite)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	req.Repo = resolved.Source.Name
	req.Path = resolved.Source.Path
	resp, err := sourceAdapter.Edit(ctx, req)
	if err != nil {
		return nil, err
	}
	if local, ok := a.resolver.ToLocalSource(sourceWithPath(resolved.Source, resp.Path)); ok {
		resp.Path = displayLocal(local)
	}
	return resp, nil
}

func (a *Adapter) Search(ctx context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	// Search is discovery, not browsing. Delegate directly to base adapter,
	// bypassing mount include/exclude filters.
	return a.base.Search(ctx, req)
}

func (a *Adapter) Locate(ctx context.Context, req store.LocateRequest) (*store.LocateResponse, error) {
	// Locate is discovery, not browsing. Delegate directly to base adapter,
	// bypassing mount include/exclude filters.
	return a.base.Locate(ctx, req)
}

func (a *Adapter) Glob(ctx context.Context, req store.GlobRequest) (*store.GlobResponse, error) {
	// Glob is discovery, not browsing. Delegate directly to base adapter,
	// bypassing mount include/exclude filters.
	return a.base.Glob(ctx, req)
}

func (a *Adapter) localizeNodesSource(source store.SourceRef, nodes []store.Node) {
	for i := range nodes {
		if local, ok := a.resolver.ToLocalSource(sourceWithPath(source, nodes[i].Path)); ok {
			nodes[i].Path = displayLocal(local)
			nodes[i].Name = pathBase(nodes[i].Path)
		}
	}
}

func (a *Adapter) adapterForSource(ctx context.Context, source store.SourceRef) (store.Adapter, error) {
	switch source.Kind {
	case store.SourceKindRepo:
		return a.base, nil
	case store.SourceKindDocs, store.SourceKindDocset:
		router, ok := a.base.(store.SourceRouter)
		if !ok {
			return nil, unsupportedSourceError(source)
		}
		return router.AdapterForSource(ctx, source)
	default:
		return nil, fmt.Errorf("%w: %s", store.ErrUnknownSource, source.String())
	}
}

func sourceWithPath(source store.SourceRef, p string) store.SourceRef {
	source.Path = p
	return source
}

func unsupportedSourceError(source store.SourceRef) error {
	return fmt.Errorf("%w: mount source %s is not supported yet", store.ErrNotSupported, source.Kind)
}

func pathBase(p string) string {
	if p == "" {
		return ""
	}
	return path.Base(strings.TrimSuffix(p, "/"))
}

func localizeTreeText(text, remotePath, localPath string, fullPath bool) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] == "" {
		return text
	}

	if fullPath {
		lines[0] = strings.TrimSuffix(displayLocal(localPath), "/")
		if strings.HasSuffix(text, "\n") {
			return strings.Join(lines, "\n")
		}
		return strings.TrimSuffix(strings.Join(lines, "\n"), "\n")
	}

	rootName := pathBase(localPath)
	if rootName == "" {
		rootName = pathBase(remotePath)
	}
	lines[0] = rootName + "/"
	if localPath == "" {
		lines[0] = "/"
	}
	joined := strings.Join(lines, "\n")
	if !strings.HasSuffix(text, "\n") {
		return strings.TrimSuffix(joined, "\n")
	}
	return joined
}

func (a *Adapter) needsComposite(localPath string) bool {
	base, descendants := a.resolver.overlayPlan(localPath)
	return (base == nil && len(descendants) > 0) || (base != nil && len(descendants) > 0)
}

func (a *Adapter) compositeTreeIfNeeded(ctx context.Context, localPath string, all bool) (*vfs.Tree, bool, error) {
	if !a.needsComposite(localPath) {
		return nil, false, nil
	}
	tree, err := a.buildCompositeTree(ctx, localPath, all)
	if err != nil {
		return nil, false, err
	}
	return tree, true, nil
}

func (a *Adapter) buildCompositeTree(ctx context.Context, localPath string, all bool) (*vfs.Tree, error) {
	local := cleanLocal(localPath)
	base, descendants := a.resolver.overlayPlan(local)
	if base == nil && len(descendants) == 0 {
		return nil, fmtNotFound(local)
	}

	nodes := make(map[string]store.Node)
	for _, mount := range descendants {
		addDirNode(nodes, mount.local)
	}

	if base != nil {
		resolved, err := a.resolver.Resolve(local, OpRead)
		if err != nil {
			return nil, err
		}
		if err := a.collectMountNodes(ctx, nodes, resolved.Source, moreSpecificMounts(base.local, a.resolver.mounts), all); err != nil {
			return nil, err
		}
	}

	for _, mount := range descendants {
		if err := a.collectMountNodes(ctx, nodes, mount.source, moreSpecificMounts(mount.local, a.resolver.mounts), all); err != nil {
			return nil, err
		}
	}

	list := make([]store.Node, 0, len(nodes))
	for _, node := range nodes {
		list = append(list, node)
	}
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].Path < list[j].Path
	})
	return vfs.NewFromNodes(list)
}

func (a *Adapter) collectMountNodes(ctx context.Context, nodes map[string]store.Node, source store.SourceRef, shadows []resolvedMount, all bool) error {
	sourceAdapter, err := a.adapterForSource(ctx, source)
	if err != nil {
		return err
	}
	resp, err := sourceAdapter.LS(ctx, store.LSRequest{
		Repo:      source.Name,
		Path:      source.Path,
		Recursive: true,
		All:       all,
	})
	if err != nil {
		return err
	}
	for _, node := range resp.Nodes {
		nodeSource := sourceWithPath(source, node.Path)
		local, ok := a.resolver.ToLocalSource(nodeSource)
		if !ok {
			continue
		}
		if shadowedByMount(local, node.Kind, shadows) {
			continue
		}
		node.Path = displayLocal(local)
		node.Name = pathBase(node.Path)
		nodes[node.Path] = node
	}
	return nil
}

func (a *Adapter) grepComposite(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	local := cleanLocal(req.Path)
	base, descendants := a.resolver.overlayPlan(local)
	if base == nil && len(descendants) == 0 {
		return nil, fmtNotFound(local)
	}

	type fetch struct {
		source    store.SourceRef
		mountRoot string
	}

	fetches := make([]fetch, 0, 1+len(descendants))
	if base != nil {
		resolved, err := a.resolver.Resolve(local, OpRead)
		if err != nil {
			return nil, err
		}
		fetches = append(fetches, fetch{
			source:    resolved.Source,
			mountRoot: base.local,
		})
	}
	for _, mount := range descendants {
		fetches = append(fetches, fetch{
			source:    mount.source,
			mountRoot: mount.local,
		})
	}

	matches := make([]store.Match, 0)
	seen := make(map[string]struct{})
	for _, fetch := range fetches {
		sourceAdapter, err := a.adapterForSource(ctx, fetch.source)
		if err != nil {
			return nil, err
		}
		shadows := moreSpecificMounts(fetch.mountRoot, a.resolver.mounts)
		resp, err := sourceAdapter.Grep(ctx, store.GrepRequest{
			Repo:            fetch.source.Name,
			Path:            fetch.source.Path,
			Pattern:         req.Pattern,
			Regex:           req.Regex,
			CaseInsensitive: req.CaseInsensitive,
			Invert:          req.Invert,
			WholeWord:       req.WholeWord,
			WholeLine:       req.WholeLine,
			ContextBefore:   req.ContextBefore,
			ContextAfter:    req.ContextAfter,
			All:             req.All,
			Include:         req.Include,
			Exclude:         req.Exclude,
		})
		if err != nil {
			return nil, err
		}
		for _, match := range resp.Matches {
			matchSource := sourceWithPath(fetch.source, match.Path)
			localPath, ok := a.resolver.ToLocalSource(matchSource)
			if !ok || shadowedByMount(localPath, "file", shadows) {
				continue
			}
			match.Path = displayLocal(localPath)
			key := match.Path + "\x00" + strconv.Itoa(match.Line) + "\x00" + match.Text
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			matches = append(matches, match)
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		if matches[i].Line != matches[j].Line {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Text < matches[j].Text
	})

	return &store.GrepResponse{Matches: matches}, nil
}

func addDirNode(nodes map[string]store.Node, local string) {
	display := displayLocal(local)
	if _, ok := nodes[display]; ok {
		return
	}
	nodes[display] = store.Node{
		Path: display,
		Name: pathBase(display),
		Kind: "dir",
	}
}

func moreSpecificMounts(local string, mounts []resolvedMount) []resolvedMount {
	local = cleanLocal(local)
	out := make([]resolvedMount, 0)
	for _, mount := range mounts {
		if strings.HasPrefix(mount.local, local+"/") {
			out = append(out, mount)
		}
	}
	return out
}

func shadowedByMount(localPath, kind string, shadows []resolvedMount) bool {
	localPath = cleanLocal(localPath)
	for _, shadow := range shadows {
		if localPath == shadow.local {
			return kind != "dir"
		}
		if underLocal(shadow.local, localPath) {
			return true
		}
	}
	return false
}

func fmtNotFound(local string) error {
	return fmt.Errorf("%w: %s", store.ErrNotFound, local)
}

func (a *Adapter) BatchHashes(ctx context.Context, req store.HashRequest) (*store.HashResponse, error) {
	resolved, err := a.resolver.Resolve(req.Path, OpRead)
	if err != nil {
		return nil, err
	}
	sourceAdapter, err := a.adapterForSource(ctx, resolved.Source)
	if err != nil {
		return nil, err
	}
	resp, err := sourceAdapter.BatchHashes(ctx, store.HashRequest{
		Repo: resolved.Source.Name,
		Path: resolved.Source.Path,
	})
	if err != nil {
		return nil, err
	}
	// Map remote paths to local display paths
	for i, ch := range resp.Hashes {
		if local, ok := a.resolver.ToLocalSource(sourceWithPath(resolved.Source, ch.Path)); ok {
			resp.Hashes[i].Path = displayLocal(local)
		}
	}
	return resp, nil
}

// paginateNodes applies limit/offset to a node slice.
func paginateNodes(nodes []store.Node, limit, offset int) []store.Node {
	if offset < 0 {
		offset = 0
	}
	if offset > len(nodes) {
		offset = len(nodes)
	}
	nodes = nodes[offset:]
	if limit > 0 && len(nodes) > limit {
		nodes = nodes[:limit]
	}
	return nodes
}
