package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gxfs/internal/store"
)

func NewHandler(adapter store.Adapter) http.Handler {
	inv, _ := adapter.(store.CacheInvalidator)
	return &handler{adapter: adapter, invalidator: inv}
}

type handler struct {
	adapter     store.Adapter
	invalidator store.CacheInvalidator
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
		return
	}

	if r.Method == http.MethodDelete && r.URL.Path == "/v1/cache" {
		if h.invalidator != nil {
			h.invalidator.Invalidate()
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	repo, op, ok := parsePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	var resp any
	var err error

	switch r.Method {
	case http.MethodGet:
		resp, err = h.dispatchRead(r, repo, op)
	case http.MethodPut:
		resp, err = h.dispatchPut(r, repo, op)
	case http.MethodDelete:
		resp, err = h.dispatchDelete(r, repo, op)
	default:
		writeJSONErrorCode(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	if err != nil {
		writeJSONError(w, err)
		return
	}
	writeJSON(w, resp)
}

func (h *handler) dispatchRead(r *http.Request, repo, op string) (any, error) {
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

func (h *handler) dispatchPut(r *http.Request, repo, op string) (any, error) {
	q := r.URL.Query()
	switch op {
	case "write":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		return h.adapter.Put(r.Context(), store.PutRequest{
			Repo:    repo,
			Path:    queryPath(q),
			Content: string(body),
		})
	case "edit":
		var editReq struct {
			Old  string `json:"old"`
			New  string `json:"new"`
			All  bool   `json:"all"`
		}
		if err := json.NewDecoder(r.Body).Decode(&editReq); err != nil {
			return nil, fmt.Errorf("decode edit body: %w", err)
		}
		return h.adapter.Edit(r.Context(), store.EditRequest{
			Repo: repo,
			Path: queryPath(q),
			Old:  editReq.Old,
			New:  editReq.New,
			All:  editReq.All,
		})
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}

func (h *handler) dispatchDelete(r *http.Request, repo, op string) (any, error) {
	q := r.URL.Query()
	switch op {
	case "delete":
		return h.adapter.Delete(r.Context(), store.DeleteRequest{
			Repo: repo,
			Path: queryPath(q),
		})
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

func writeJSONError(w http.ResponseWriter, err error) {
	status, code := mapError(err)
	writeJSONErrorCode(w, status, code, err.Error())
}

func writeJSONErrorCode(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func mapError(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrOldNotFound):
		return http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, store.ErrReadOnlyMount):
		return http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, store.ErrIsDir), errors.Is(err, store.ErrNotDir),
		errors.Is(err, store.ErrEmptyOld), errors.Is(err, store.ErrCannotDeleteRoot):
		return http.StatusBadRequest, "BAD_REQUEST"
	case errors.Is(err, store.ErrContentNotReady):
		return http.StatusNotFound, "CONTENT_NOT_READY"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}
