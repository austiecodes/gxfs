package mount

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"gxfs/internal/config"
	"gxfs/internal/store"
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
	local      string
	remoteRepo string
	remoteRoot string
	mode       string
}

type ResolvedPath struct {
	LocalPath  string
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
		remoteRepo, remoteRoot, err := parseRemote(repo, m.Remote)
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
			local:      local,
			remoteRepo: remoteRepo,
			remoteRoot: remoteRoot,
			mode:       mode,
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

		remotePath := m.remoteRoot
		if rel != "" {
			remotePath = path.Join(remotePath, rel)
		}
		if !strings.HasPrefix(remotePath, "/") {
			remotePath = "/" + remotePath
		}

		return ResolvedPath{
			LocalPath:  local,
			RemoteRepo: m.remoteRepo,
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

func (r *Resolver) ToLocal(remoteRepo, remotePath string) (string, bool) {
	remotePath = cleanRemote(remotePath)
	for _, m := range r.mounts {
		if remoteRepo != m.remoteRepo {
			continue
		}
		if !underRemote(m.remoteRoot, remotePath) {
			continue
		}
		rel := strings.TrimPrefix(remotePath, m.remoteRoot)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return m.local, true
		}
		return path.Join(m.local, rel), true
	}
	return "", false
}

func parseRemote(currentRepo, raw string) (string, string, error) {
	const selfPrefix = "repo://self/"
	switch {
	case raw == "repo://self":
		return "", "", fmt.Errorf("remote %q needs a path after self/ (e.g. repo://self/docs)", raw)
	case strings.HasPrefix(raw, selfPrefix):
		remoteRoot := cleanRemote(strings.TrimPrefix(raw, selfPrefix))
		if remoteRoot == "/" {
			return "", "", fmt.Errorf("self root mount is not allowed")
		}
		return currentRepo, remoteRoot, nil
	case strings.HasPrefix(raw, "collection://"):
		return "", "", fmt.Errorf("collection mounts are not supported in phase 1")
	case strings.HasPrefix(raw, "repo://"):
		rest := strings.TrimPrefix(raw, "repo://")
		// Split at the first unencoded '/' to separate repo name from path.
		// Repo names containing '/' (e.g. "github/openai-go") must be URL-encoded
		// in the ref: repo://github%2Fopenai-go/docs
		parts := strings.SplitN(rest, "/", 2)
		if parts[0] == "" {
			return "", "", fmt.Errorf("remote %q needs a repo name after repo://", raw)
		}
		remoteRepo, err := url.PathUnescape(parts[0])
		if err != nil {
			return "", "", fmt.Errorf("remote %q has invalid repo name: %w", raw, err)
		}
		remotePath := "/"
		if len(parts) == 2 && parts[1] != "" {
			remotePath = cleanRemote(parts[1])
		}
		// Reject self root mount (same as the selfPrefix branch above)
		if remoteRepo == currentRepo && remotePath == "/" {
			return "", "", fmt.Errorf("self root mount is not allowed")
		}
		return remoteRepo, remotePath, nil
	default:
		return "", "", fmt.Errorf("unsupported remote %q", raw)
	}
}

// ParseRemoteRef parses a remote reference string (e.g. "repo://self/docs"
// or "repo://other-repo/") into the target repo name and remote path.
// It is the public entry point for parsing remote refs in CLI commands.
func ParseRemoteRef(currentRepo, raw string) (repo, remotePath string, err error) {
	return parseRemote(currentRepo, raw)
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
