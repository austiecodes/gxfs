package command

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	mountadapter "github.com/austiecodes/gxfs/internal/mount"
)

func argPath(args []string, fallback string) string {
	if len(args) == 0 || args[0] == "" {
		return fallback
	}
	return args[0]
}

// parseRepoRef parses a "repo://<name>/<path>" argument into repo and path.
// Delegates to mountadapter.ParseRemoteRef for consistent URL decoding and
// cross-repo handling. Handles repo://self/<path> by keeping repo as "self".
// Returns ("", "", false) if the argument is not a repo ref or is invalid.
func parseRepoRef(currentRepo, arg string) (repo, p string, ok bool) {
	if !strings.HasPrefix(arg, "repo://") {
		return "", "", false
	}
	r, p, err := mountadapter.ParseRemoteRef(currentRepo, arg)
	if err != nil {
		return "", "", false
	}
	return r, p, true
}

// repoLister is a CLI-local interface for repo discovery with error propagation.
// The client.Client satisfies this via its RepoList method.
type repoLister interface {
	RepoList(ctx context.Context) ([]string, error)
}

func cleanLocalDocsPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return ""
	}
	return filepath.ToSlash(path.Clean(p))
}
