package store

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

type Registry struct {
	repos map[string]Adapter
	docs  map[string]Adapter
}

var _ Adapter = (*Registry)(nil)
var _ CacheInvalidator = (*Registry)(nil)
var _ MountSourceLister = (*Registry)(nil)
var _ SourceRouter = (*Registry)(nil)

const (
	rootPath    = "/"
	reposPath   = "/repos"
	docsetsPath = "/docsets"

	nodeKindDir = "dir"
)

func NewRegistry(adapters map[string]Adapter) (*Registry, error) {
	return NewNamespaceRegistry(adapters, nil)
}

func NewNamespaceRegistry(repos map[string]Adapter, docs map[string]Adapter) (*Registry, error) {
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
	if req.Repo == "" {
		return r.lsRoot(ctx, req)
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.LS(ctx, req)
}

func (r *Registry) Tree(ctx context.Context, req TreeRequest) (*TreeResponse, error) {
	if req.Repo == "" {
		return r.treeRoot(ctx, req)
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Tree(ctx, req)
}

func (r *Registry) Cat(ctx context.Context, req CatRequest) (*CatResponse, error) {
	if req.Repo == "" {
		return r.catRoot(ctx, req)
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Cat(ctx, req)
}

func (r *Registry) Grep(ctx context.Context, req GrepRequest) (*GrepResponse, error) {
	if req.Repo == "" {
		route, err := r.requireSourceRoute(req.Path)
		if err != nil {
			return nil, err
		}
		req.Repo = route.name
		req.Path = route.remotePath
		resp, err := route.adapter.Grep(ctx, req)
		if err != nil {
			return nil, err
		}
		for i := range resp.Matches {
			resp.Matches[i].Path = route.localPath(resp.Matches[i].Path)
		}
		return resp, nil
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Grep(ctx, req)
}

func (r *Registry) Find(ctx context.Context, req FindRequest) (*FindResponse, error) {
	if req.Repo == "" {
		route, err := r.requireSourceRoute(req.Path)
		if err != nil {
			return nil, err
		}
		req.Repo = route.name
		req.Path = route.remotePath
		resp, err := route.adapter.Find(ctx, req)
		if err != nil {
			return nil, err
		}
		route.localizeNodes(resp.Nodes)
		return resp, nil
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Find(ctx, req)
}

func (r *Registry) Stat(ctx context.Context, req StatRequest) (*StatResponse, error) {
	if req.Repo == "" {
		return r.statRoot(ctx, req)
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Stat(ctx, req)
}

func (r *Registry) Put(ctx context.Context, req PutRequest) (*PutResponse, error) {
	if req.Repo == "" {
		route, err := r.requireWritableSourceRoute(req.Path)
		if err != nil {
			return nil, err
		}
		req.Repo = route.name
		req.Path = route.remotePath
		resp, err := route.adapter.Put(ctx, req)
		if err != nil {
			return nil, err
		}
		resp.Node.Path = route.localPath(resp.Node.Path)
		resp.Node.Name = path.Base(resp.Node.Path)
		return resp, nil
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Put(ctx, req)
}

func (r *Registry) Delete(ctx context.Context, req DeleteRequest) (*DeleteResponse, error) {
	if req.Repo == "" {
		route, err := r.requireWritableSourceRoute(req.Path)
		if err != nil {
			return nil, err
		}
		req.Repo = route.name
		req.Path = route.remotePath
		return route.adapter.Delete(ctx, req)
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Delete(ctx, req)
}

func (r *Registry) Edit(ctx context.Context, req EditRequest) (*EditResponse, error) {
	if req.Repo == "" {
		route, err := r.requireWritableSourceRoute(req.Path)
		if err != nil {
			return nil, err
		}
		req.Repo = route.name
		req.Path = route.remotePath
		resp, err := route.adapter.Edit(ctx, req)
		if err != nil {
			return nil, err
		}
		resp.Path = route.localPath(resp.Path)
		return resp, nil
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Edit(ctx, req)
}

func (r *Registry) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	if req.Repo == "" {
		route, err := r.requireSourceRoute(req.Path)
		if err != nil {
			return nil, err
		}
		req.Repo = route.name
		req.Path = route.remotePath
		resp, err := route.adapter.Search(ctx, req)
		if err != nil {
			return nil, err
		}
		for i := range resp.Results {
			resp.Results[i].Path = route.localPath(resp.Results[i].Path)
		}
		return resp, nil
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Search(ctx, req)
}

func (r *Registry) Locate(ctx context.Context, req LocateRequest) (*LocateResponse, error) {
	if req.Repo == "" {
		return nil, fmt.Errorf("%w: unified root locate", ErrNotSupported)
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.Locate(ctx, req)
}

func (r *Registry) BatchHashes(ctx context.Context, req HashRequest) (*HashResponse, error) {
	if req.Repo == "" {
		route, err := r.requireSourceRoute(req.Path)
		if err != nil {
			return nil, err
		}
		req.Repo = route.name
		req.Path = route.remotePath
		resp, err := route.adapter.BatchHashes(ctx, req)
		if err != nil {
			return nil, err
		}
		for i := range resp.Hashes {
			resp.Hashes[i].Path = route.localPath(resp.Hashes[i].Path)
		}
		return resp, nil
	}
	adapter, err := r.adapter(req.Repo)
	if err != nil {
		return nil, err
	}
	return adapter.BatchHashes(ctx, req)
}

func (r *Registry) Glob(ctx context.Context, req GlobRequest) (*GlobResponse, error) {
	if req.Repo == "" {
		return nil, fmt.Errorf("%w: unified root glob", ErrNotSupported)
	}
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

type sourceRoute struct {
	name       string
	adapter    Adapter
	localRoot  string
	remotePath string
}

func (r *Registry) lsRoot(ctx context.Context, req LSRequest) (*LSResponse, error) {
	p := cleanRegistryPath(req.Path)
	if nodes, ok := r.virtualChildren(p, req.Recursive); ok {
		total := len(nodes)
		nodes = sortAndPageNodes(nodes, req.Sort, req.Reverse, req.Limit, req.Offset)
		return &LSResponse{Nodes: nodes, Total: total}, nil
	}

	route, ok := r.sourceRoute(p)
	if !ok {
		return nil, notFound(p)
	}
	req.Repo = route.name
	req.Path = route.remotePath
	resp, err := route.adapter.LS(ctx, req)
	if err != nil {
		return nil, err
	}
	route.localizeNodes(resp.Nodes)
	return resp, nil
}

func (r *Registry) treeRoot(ctx context.Context, req TreeRequest) (*TreeResponse, error) {
	p := cleanRegistryPath(req.Path)
	if p == rootPath || p == reposPath || p == docsetsPath {
		return r.virtualTree(p, req), nil
	}

	route, ok := r.sourceRoute(p)
	if !ok {
		if _, ok := r.virtualNode(p); ok {
			return r.virtualTree(p, req), nil
		}
		return nil, notFound(p)
	}
	req.Repo = route.name
	req.Path = route.remotePath
	resp, err := route.adapter.Tree(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.Root.Path = route.localPath(resp.Root.Path)
	resp.Root.Name = path.Base(resp.Root.Path)
	resp.Text = route.localizeTreeText(resp.Text, cleanRegistryPath(req.Path), req.FullPath, req.ShowSize)
	return resp, nil
}

func (r *Registry) catRoot(ctx context.Context, req CatRequest) (*CatResponse, error) {
	p := cleanRegistryPath(req.Path)
	if _, ok := r.virtualNode(p); ok {
		return nil, fmt.Errorf("%w: %s", ErrIsDir, p)
	}

	route, ok := r.sourceRoute(p)
	if !ok {
		return nil, notFound(p)
	}
	req.Repo = route.name
	req.Path = route.remotePath
	resp, err := route.adapter.Cat(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.Path = route.localPath(resp.Path)
	return resp, nil
}

func (r *Registry) statRoot(ctx context.Context, req StatRequest) (*StatResponse, error) {
	p := cleanRegistryPath(req.Path)
	if node, ok := r.virtualNode(p); ok {
		return &StatResponse{Node: node}, nil
	}

	route, ok := r.sourceRoute(p)
	if !ok {
		return nil, notFound(p)
	}
	req.Repo = route.name
	req.Path = route.remotePath
	resp, err := route.adapter.Stat(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.Node.Path = route.localPath(resp.Node.Path)
	resp.Node.Name = path.Base(resp.Node.Path)
	return resp, nil
}

func (r *Registry) requireSourceRoute(p string) (sourceRoute, error) {
	clean := cleanRegistryPath(p)
	route, ok := r.sourceRoute(clean)
	if ok {
		return route, nil
	}
	if _, ok := r.virtualNode(clean); ok {
		return sourceRoute{}, fmt.Errorf("%w: %s", ErrNotSupported, clean)
	}
	return sourceRoute{}, notFound(clean)
}

func (r *Registry) requireWritableSourceRoute(p string) (sourceRoute, error) {
	route, err := r.requireSourceRoute(p)
	if err != nil {
		if errors.Is(err, ErrNotSupported) {
			return sourceRoute{}, fmt.Errorf("%w: %s", ErrReadOnlyMount, cleanRegistryPath(p))
		}
		return sourceRoute{}, err
	}
	if route.remotePath == rootPath {
		return sourceRoute{}, fmt.Errorf("%w: %s", ErrReadOnlyMount, cleanRegistryPath(p))
	}
	return route, nil
}

func (r *Registry) sourceRoute(p string) (sourceRoute, bool) {
	if route, ok := sourceRouteForNamespace(p, reposPath, r.repos); ok {
		return route, true
	}
	if route, ok := sourceRouteForNamespace(p, docsetsPath, r.docs); ok {
		return route, true
	}
	return sourceRoute{}, false
}

func sourceRouteForNamespace(p, base string, adapters map[string]Adapter) (sourceRoute, bool) {
	rel, ok := namespaceRel(p, base)
	if !ok || rel == "" {
		return sourceRoute{}, false
	}

	var best string
	for name := range adapters {
		if rel == name || strings.HasPrefix(rel, name+"/") {
			if len(name) > len(best) {
				best = name
			}
		}
	}
	if best == "" {
		return sourceRoute{}, false
	}

	remote := rootPath
	if rel != best {
		remote = "/" + strings.TrimPrefix(rel, best+"/")
	}
	return sourceRoute{
		name:       best,
		adapter:    adapters[best],
		localRoot:  base + "/" + best,
		remotePath: cleanRegistryPath(remote),
	}, true
}

func (r *Registry) virtualNode(p string) (Node, bool) {
	p = cleanRegistryPath(p)
	switch p {
	case rootPath:
		return dirNode(rootPath), true
	case reposPath, docsetsPath:
		return dirNode(p), true
	}

	if virtualNamespaceNode(p, reposPath, r.repos) || virtualNamespaceNode(p, docsetsPath, r.docs) {
		return dirNode(p), true
	}
	return Node{}, false
}

func virtualNamespaceNode(p, base string, adapters map[string]Adapter) bool {
	rel, ok := namespaceRel(p, base)
	if !ok || rel == "" {
		return false
	}
	for name := range adapters {
		if rel == name || strings.HasPrefix(name, rel+"/") {
			return true
		}
	}
	return false
}

func (r *Registry) virtualChildren(p string, recursive bool) ([]Node, bool) {
	p = cleanRegistryPath(p)
	if p == rootPath {
		nodes := []Node{dirNode(reposPath), dirNode(docsetsPath)}
		if !recursive {
			return nodes, true
		}
		return append(nodes, r.virtualDescendants(rootPath)...), true
	}
	if p == reposPath {
		return namespaceChildren(p, reposPath, r.repos, recursive), true
	}
	if p == docsetsPath {
		return namespaceChildren(p, docsetsPath, r.docs, recursive), true
	}
	if _, ok := r.sourceRoute(p); ok {
		return nil, false
	}
	if virtualNamespaceNode(p, reposPath, r.repos) {
		return namespaceChildren(p, reposPath, r.repos, recursive), true
	}
	if virtualNamespaceNode(p, docsetsPath, r.docs) {
		return namespaceChildren(p, docsetsPath, r.docs, recursive), true
	}
	return nil, false
}

func (r *Registry) virtualDescendants(p string) []Node {
	nodes := make(map[string]Node)
	for _, node := range namespaceChildren(reposPath, reposPath, r.repos, true) {
		nodes[node.Path] = node
	}
	for _, node := range namespaceChildren(docsetsPath, docsetsPath, r.docs, true) {
		nodes[node.Path] = node
	}

	out := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		if p == rootPath || strings.HasPrefix(node.Path, p+"/") {
			out = append(out, node)
		}
	}
	sortNodes(out, "", false)
	return out
}

func namespaceChildren(p, base string, adapters map[string]Adapter, recursive bool) []Node {
	rel, ok := namespaceRel(p, base)
	if !ok {
		return nil
	}

	nodes := make(map[string]Node)
	for name := range adapters {
		if rel != "" && name != rel && !strings.HasPrefix(name, rel+"/") {
			continue
		}

		segments := strings.Split(name, "/")
		start := 0
		if rel != "" {
			start = len(strings.Split(rel, "/"))
		}
		if start >= len(segments) {
			continue
		}

		end := start + 1
		if recursive {
			end = len(segments)
		}
		for i := start; i < end; i++ {
			childRel := strings.Join(segments[:i+1], "/")
			childPath := base + "/" + childRel
			nodes[childPath] = dirNode(childPath)
		}
	}

	out := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
	}
	sortNodes(out, "", false)
	return out
}

func (r *Registry) virtualTree(p string, req TreeRequest) *TreeResponse {
	nodes := map[string]Node{
		rootPath:    dirNode(rootPath),
		reposPath:   dirNode(reposPath),
		docsetsPath: dirNode(docsetsPath),
	}
	for _, node := range namespaceChildren(reposPath, reposPath, r.repos, true) {
		nodes[node.Path] = node
	}
	for _, node := range namespaceChildren(docsetsPath, docsetsPath, r.docs, true) {
		nodes[node.Path] = node
	}

	children := make(map[string][]Node)
	for nodePath, node := range nodes {
		if nodePath == rootPath {
			continue
		}
		parent := path.Dir(nodePath)
		children[parent] = append(children[parent], node)
	}
	for parent := range children {
		sortNodes(children[parent], req.Sort, false)
	}

	root := nodes[p]
	var out strings.Builder
	writeTreeLine(&out, root, 0, req.FullPath, req.ShowSize)
	writeVirtualTree(&out, root.Path, children, 1, req.Depth, req)
	return &TreeResponse{Root: root, Text: out.String()}
}

func writeVirtualTree(out *strings.Builder, dir string, children map[string][]Node, level, depth int, req TreeRequest) {
	if depth >= 0 && level > depth {
		return
	}

	nodes := children[dir]
	if req.DirsOnly {
		filtered := nodes[:0]
		for _, node := range nodes {
			if node.Kind == nodeKindDir {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}

	for _, node := range nodes {
		writeTreeLine(out, node, level, req.FullPath, req.ShowSize)
		if node.Kind == nodeKindDir {
			writeVirtualTree(out, node.Path, children, level+1, depth, req)
		}
	}
}

func writeTreeLine(out *strings.Builder, node Node, level int, fullPath, showSize bool) {
	out.WriteString(strings.Repeat("  ", level))
	if fullPath || node.Path == rootPath {
		out.WriteString(node.Path)
	} else {
		out.WriteString(node.Name)
	}
	if level > 0 && node.Kind == nodeKindDir {
		out.WriteByte('/')
	}
	if showSize && node.Kind != nodeKindDir {
		fmt.Fprintf(out, " [%d]", node.Size)
	}
	out.WriteByte('\n')
}

func namespaceRel(p, base string) (string, bool) {
	p = cleanRegistryPath(p)
	if p == base {
		return "", true
	}
	if !strings.HasPrefix(p, base+"/") {
		return "", false
	}
	return strings.TrimPrefix(p, base+"/"), true
}

func (route sourceRoute) localizeNodes(nodes []Node) {
	for i := range nodes {
		nodes[i].Path = route.localPath(nodes[i].Path)
		nodes[i].Name = path.Base(nodes[i].Path)
	}
}

func (route sourceRoute) localPath(remote string) string {
	remote = cleanRegistryPath(remote)
	if remote == rootPath {
		return route.localRoot
	}
	return route.localRoot + remote
}

func (route sourceRoute) localizeTreeText(text, localRoot string, fullPath bool, showSize bool) string {
	if !fullPath {
		return localizeTreeRoot(text, localRoot, false)
	}
	if text == "" {
		return text
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		indentLen := len(line) - len(strings.TrimLeft(line, " "))
		indent := line[:indentLen]
		rest := line[indentLen:]
		token, suffix := splitTreeFullPathToken(rest, showSize)
		if token == "" || !strings.HasPrefix(token, "/") {
			continue
		}

		trailingSlash := strings.HasSuffix(token, "/") && token != "/"
		remotePath := token
		if trailingSlash {
			remotePath = strings.TrimSuffix(remotePath, "/")
		}

		localPath := route.localPath(remotePath)
		if trailingSlash {
			localPath += "/"
		}
		lines[i] = indent + localPath + suffix
	}
	return strings.Join(lines, "\n")
}

func localizeTreeRoot(text, localRoot string, fullPath bool) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] == "" {
		return text
	}
	if fullPath {
		lines[0] = localRoot
	} else if localRoot == rootPath {
		lines[0] = rootPath
	} else {
		lines[0] = path.Base(localRoot) + "/"
	}
	return strings.Join(lines, "\n")
}

func splitTreeFullPathToken(rest string, showSize bool) (token string, suffix string) {
	if i := strings.LastIndex(rest, " ["); showSize && i >= 0 && strings.HasSuffix(rest, "]") {
		return rest[:i], rest[i:]
	}
	return rest, ""
}

func sortAndPageNodes(nodes []Node, sortField string, reverse bool, limit, offset int) []Node {
	nodes = append([]Node(nil), nodes...)
	sortNodes(nodes, sortField, reverse)
	if offset < 0 {
		offset = 0
	}
	if offset > len(nodes) {
		offset = len(nodes)
	}
	nodes = nodes[offset:]
	if limit > 0 && limit < len(nodes) {
		nodes = nodes[:limit]
	}
	return nodes
}

func sortNodes(nodes []Node, sortField string, reverse bool) {
	sort.SliceStable(nodes, func(i, j int) bool {
		cmp := strings.Compare(nodes[i].Name, nodes[j].Name)
		switch sortField {
		case "size":
			if nodes[i].Size != nodes[j].Size {
				cmp = -1
				if nodes[i].Size > nodes[j].Size {
					cmp = 1
				}
			}
		case "mtime":
			if nodes[i].ModTime != nodes[j].ModTime {
				cmp = strings.Compare(nodes[i].ModTime, nodes[j].ModTime)
			}
		}
		if cmp == 0 {
			cmp = strings.Compare(nodes[i].Path, nodes[j].Path)
		}
		if reverse {
			return cmp > 0
		}
		return cmp < 0
	})
}

func dirNode(p string) Node {
	p = cleanRegistryPath(p)
	return Node{Path: p, Name: path.Base(p), Kind: nodeKindDir}
}

func cleanRegistryPath(p string) string {
	if p == "" {
		return rootPath
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

func notFound(p string) error {
	return fmt.Errorf("%w: %s", ErrNotFound, p)
}
