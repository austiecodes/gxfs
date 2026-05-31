package store

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// SourceKind identifies the namespace a mount source points at.
type SourceKind string

const (
	SourceKindRepo   SourceKind = "repo"
	SourceKindDocs   SourceKind = "docs"
	SourceKindDocset SourceKind = "docset"
)

// SourceRef is a typed reference to a mountable source namespace.
type SourceRef struct {
	Kind SourceKind
	Name string
	Path string
}

// ParseSourceRef parses a source URI such as repo://self/docs,
// docs://openai-go-sdk, or docset://best-practices/usage.md.
func ParseSourceRef(raw string) (SourceRef, error) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(raw), "://")
	if !ok {
		return SourceRef{}, fmt.Errorf("unsupported source %q", raw)
	}

	kind := SourceKind(scheme)
	switch kind {
	case SourceKindRepo, SourceKindDocs, SourceKindDocset:
	case "collection":
		return SourceRef{}, fmt.Errorf("collection mounts are not supported")
	default:
		return SourceRef{}, fmt.Errorf("unsupported source %q", raw)
	}

	namePart, pathPart, _ := strings.Cut(rest, "/")
	if namePart == "" {
		return SourceRef{}, fmt.Errorf("source %q needs a name after %s://", raw, kind)
	}
	name, err := url.PathUnescape(namePart)
	if err != nil {
		return SourceRef{}, fmt.Errorf("source %q has invalid name: %w", raw, err)
	}

	sourcePath := "/"
	if pathPart != "" {
		sourcePath = cleanSourcePath(pathPart)
	}
	return SourceRef{Kind: kind, Name: name, Path: sourcePath}, nil
}

func (s SourceRef) String() string {
	name := url.PathEscape(s.Name)
	if s.Path == "" || s.Path == "/" {
		return string(s.Kind) + "://" + name
	}
	return string(s.Kind) + "://" + name + cleanSourcePath(s.Path)
}

func cleanSourcePath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}
