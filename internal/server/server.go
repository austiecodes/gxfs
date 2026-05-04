package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gxfs/internal/store"
)

func NewHandler(adapter store.Adapter) http.Handler {
	return &handler{adapter: adapter}
}

type handler struct {
	adapter store.Adapter
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
		return
	}

	repo, op, ok := parsePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	resp, err := h.dispatch(r, repo, op)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, resp)
}

func (h *handler) dispatch(r *http.Request, repo, op string) (any, error) {
	q := r.URL.Query()
	switch op {
	case "ls":
		return h.adapter.LS(r.Context(), store.LSRequest{
			Repo:      repo,
			Path:      queryPath(q),
			Sort:      q.Get("sort"),
			Reverse:   queryBoolOr(q, "reverse"),
			Recursive: queryBoolOr(q, "recursive"),
			All:       queryBoolOr(q, "all"),
		})
	case "tree":
		depth, err := queryInt(q, "depth")
		if err != nil {
			return nil, err
		}
		return h.adapter.Tree(r.Context(), store.TreeRequest{
			Repo:      repo,
			Path:      queryPath(q),
			Depth:     depth,
			All:       queryBoolOr(q, "all"),
			DirsOnly:  queryBoolOr(q, "dirs_only"),
			FullPath:  queryBoolOr(q, "full_path"),
			ShowSize:  queryBoolOr(q, "show_size"),
			Sort:      q.Get("sort"),
			DirsFirst: queryBoolOr(q, "dirs_first"),
		})
	case "cat":
		return h.adapter.Cat(r.Context(), store.CatRequest{Repo: repo, Path: queryPath(q)})
	case "grep":
		regex, err := queryBool(q, "regex")
		if err != nil {
			return nil, err
		}
		ctxBefore, _ := queryInt(q, "context_before")
		ctxAfter, _ := queryInt(q, "context_after")
		return h.adapter.Grep(r.Context(), store.GrepRequest{
			Repo:            repo,
			Path:            queryPath(q),
			Pattern:         q.Get("pattern"),
			Regex:           regex,
			CaseInsensitive: queryBoolOr(q, "case_insensitive"),
			Invert:          queryBoolOr(q, "invert"),
			WholeWord:       queryBoolOr(q, "whole_word"),
			WholeLine:       queryBoolOr(q, "whole_line"),
			ContextBefore:   ctxBefore,
			ContextAfter:    ctxAfter,
			All:             queryBoolOr(q, "all"),
			Include:         q.Get("include"),
			Exclude:         q.Get("exclude"),
		})
	case "find":
		maxDepth, _ := queryInt(q, "maxdepth")
		minDepth, _ := queryInt(q, "mindepth")
		return h.adapter.Find(r.Context(), store.FindRequest{
			Repo:     repo,
			Path:     queryPath(q),
			Name:     q.Get("name"),
			Type:     q.Get("type"),
			MaxDepth: maxDepth,
			MinDepth: minDepth,
			All:      queryBoolOr(q, "all"),
			IName:    q.Get("iname"),
		})
	case "stat":
		return h.adapter.Stat(r.Context(), store.StatRequest{Repo: repo, Path: queryPath(q)})
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}

func parsePath(p string) (repo string, op string, ok bool) {
	rest := strings.TrimPrefix(p, "/v1/repos/")
	if rest == p {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	repo, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", false
	}
	return repo, parts[1], true
}

func queryPath(q url.Values) string {
	if p := q.Get("path"); p != "" {
		return p
	}
	return "/"
}

func queryInt(q url.Values, key string) (int, error) {
	raw := q.Get(key)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return value, nil
}

func queryBool(q url.Values, key string) (bool, error) {
	raw := q.Get(key)
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %w", key, err)
	}
	return value, nil
}

func queryBoolOr(q url.Values, key string) bool {
	v, _ := queryBool(q, key)
	return v
}

func writeJSON(w http.ResponseWriter, resp any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
