package mount

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/austiecodes/gxfs/internal/config"
	"github.com/austiecodes/gxfs/internal/store"
)

type Operation string

const (
	OpRead  Operation = "read"
	OpWrite Operation = "write"
)

type Resolver struct {
	repo   string
	mounts []resolvedMount
}

type resolvedMount struct {
	local  string
	source store.SourceRef
	mode   string
}

type ResolvedPath struct {
	LocalPath  string
	Source     store.SourceRef
	RemoteRepo string
	RemotePath string
	Mode       string
}

func NewResolver(repo string, mounts []config.MountConfig) (*Resolver, error) {
	if repo == "" {
		return nil, fmt.Errorf("repo is required")
	}

	resolved := make([]resolvedMount, 0, len(mounts))
	for i, m := range mounts {
		local := cleanLocal(m.Local)
		if local == "" {
			return nil, fmt.Errorf("mounts[%d].local is required", i)
		}
		source, err := parseRemote(repo, m.Remote)
		if err != nil {
			return nil, fmt.Errorf("mounts[%d].remote: %w", i, err)
		}
		mode := m.Mode
		if mode == "" {
			mode = "readonly"
		}
		if mode != "readonly" && mode != "writable" {
			return nil, fmt.Errorf("mounts[%d].mode must be readonly or writable", i)
		}
		resolved = append(resolved, resolvedMount{
			local:  local,
			source: source,
			mode:   mode,
		})
	}

	sort.SliceStable(resolved, func(i, j int) bool {
		return len(resolved[i].local) > len(resolved[j].local)
	})

	return &Resolver{repo: repo, mounts: resolved}, nil
}

func (r *Resolver) Resolve(localPath string, op Operation) (ResolvedPath, error) {
	local := cleanLocal(localPath)
	for _, m := range r.mounts {
		if !underLocal(m.local, local) {
			continue
		}
		if op == OpWrite && m.mode != "writable" {
			return ResolvedPath{}, fmt.Errorf("%w: %s", store.ErrReadOnlyMount, local)
		}

		rel := strings.TrimPrefix(local, m.local)
		rel = strings.TrimPrefix(rel, "/")

		source := m.source
		remotePath := source.Path
		if rel != "" {
			remotePath = path.Join(remotePath, rel)
		}
		if !strings.HasPrefix(remotePath, "/") {
			remotePath = "/" + remotePath
		}
		source.Path = remotePath

		return ResolvedPath{
			LocalPath:  local,
			Source:     source,
			RemoteRepo: remoteRepoCompat(source),
			RemotePath: remotePath,
			Mode:       m.mode,
		}, nil
	}

	return ResolvedPath{}, fmt.Errorf("%w: %s", store.ErrNotFound, local)
}

func (r *Resolver) overlayPlan(localPath string) (*resolvedMount, []resolvedMount) {
	local := cleanLocal(localPath)

	var base *resolvedMount
	for i := range r.mounts {
		m := &r.mounts[i]
		if underLocal(m.local, local) && (base == nil || len(m.local) > len(base.local)) {
			base = m
		}
	}

	var descendants []resolvedMount
	for _, m := range r.mounts {
		if local == "" {
			descendants = append(descendants, m)
			continue
		}
		if strings.HasPrefix(m.local, local+"/") {
			descendants = append(descendants, m)
		}
	}

	return base, descendants
}

func (r *Resolver) hasVirtualDir(localPath string) bool {
	base, descendants := r.overlayPlan(localPath)
	return base == nil && len(descendants) > 0
}

// MountLocals returns the local paths of all configured mounts.
func (r *Resolver) MountLocals() []string {
	locals := make([]string, len(r.mounts))
	for i, m := range r.mounts {
		locals[i] = m.local
	}
	return locals
}

func (r *Resolver) ToLocal(remoteRepo, remotePath string) (string, bool) {
	return r.ToLocalSource(store.SourceRef{Kind: store.SourceKindRepo, Name: remoteRepo, Path: remotePath})
}

func (r *Resolver) ToLocalSource(source store.SourceRef) (string, bool) {
	source.Path = cleanRemote(source.Path)
	for _, m := range r.mounts {
		if source.Kind != m.source.Kind || source.Name != m.source.Name {
			continue
		}
		if !underRemote(m.source.Path, source.Path) {
			continue
		}
		rel := strings.TrimPrefix(source.Path, m.source.Path)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return m.local, true
		}
		return path.Join(m.local, rel), true
	}
	return "", false
}

func parseRemote(currentRepo, raw string) (store.SourceRef, error) {
	if raw == "repo://self" {
		return store.SourceRef{}, fmt.Errorf("remote %q needs a path after self/ (e.g. repo://self/docs)", raw)
	}
	switch {
	case strings.HasPrefix(raw, "repo://"),
		strings.HasPrefix(raw, "docs://"),
		strings.HasPrefix(raw, "docset://"):
	default:
		return store.SourceRef{}, fmt.Errorf("unsupported remote %q", raw)
	}

	source, err := store.ParseSourceRef(raw)
	if err != nil {
		return store.SourceRef{}, err
	}
	if source.Kind == store.SourceKindRepo && source.Name == "self" {
		source.Name = currentRepo
		if source.Path == "/" {
			return store.SourceRef{}, fmt.Errorf("self root mount is not allowed")
		}
	}
	if source.Kind == store.SourceKindRepo && source.Name == currentRepo && source.Path == "/" {
		return store.SourceRef{}, fmt.Errorf("self root mount is not allowed")
	}
	return source, nil
}

// ParseSourceRef parses a mount source reference using the same self-repo rules
// as resolver configuration.
func ParseSourceRef(currentRepo, raw string) (store.SourceRef, error) {
	return parseRemote(currentRepo, raw)
}

// ParseRemoteRef parses a remote reference string (e.g. "repo://self/docs"
// or "repo://other-repo/") into the target repo name and remote path.
// It is the public entry point for parsing remote refs in CLI commands.
func ParseRemoteRef(currentRepo, raw string) (repo, remotePath string, err error) {
	source, err := parseRemote(currentRepo, raw)
	if err != nil {
		return "", "", err
	}
	if source.Kind != store.SourceKindRepo {
		return "", "", fmt.Errorf("unsupported remote %q", raw)
	}
	return source.Name, source.Path, nil
}

func remoteRepoCompat(source store.SourceRef) string {
	if source.Kind != store.SourceKindRepo {
		return ""
	}
	return source.Name
}

func cleanLocal(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return ""
	}
	return path.Clean(p)
}

func cleanRemote(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

func underLocal(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+"/")
}

func underRemote(root, p string) bool {
	root = cleanRemote(root)
	p = cleanRemote(p)
	if root == "/" {
		return true // root matches everything
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

func displayLocal(local string) string {
	local = cleanLocal(local)
	if local == "" {
		return "/"
	}
	return "/" + local
}
