package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/austiecodes/gxfs/internal/store"
)

func NewHandler(adapter store.Adapter, writableRepos map[string]bool) http.Handler {
	inv, _ := adapter.(store.CacheInvalidator)
	h := &handler{adapter: adapter, invalidator: inv, writableRepos: writableRepos}
	return &loggingMiddleware{next: h}
}

// NewHandlerWithDocsets creates a handler with docset support.
func NewHandlerWithDocsets(adapter store.Adapter, writableRepos map[string]bool, docsetMgr store.DocsetManager) http.Handler {
	inv, _ := adapter.(store.CacheInvalidator)
	h := &handler{adapter: adapter, invalidator: inv, writableRepos: writableRepos, docsetMgr: docsetMgr}
	return &loggingMiddleware{next: h}
}

type handler struct {
	adapter       store.Adapter
	invalidator   store.CacheInvalidator
	writableRepos map[string]bool // repos that accept cross-repo writes
	docsetMgr     store.DocsetManager
}

type repoWritableChecker interface {
	RepoWritable(repo string) bool
}

type repoWritableRefreshChecker interface {
	RepoWritableWithRefresh(ctx context.Context, repo string) (bool, error)
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

	if r.URL.Path == "/v1/repos" {
		switch r.Method {
		case http.MethodGet:
			h.handleListRepos(w, r)
		case http.MethodPost:
			h.handleRegisterRepo(w, r)
		default:
			writeJSONErrorCode(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}
		return
	}

	if r.URL.Path == "/v1/usage-events" {
		if r.Method != http.MethodPost {
			writeJSONErrorCode(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		h.handleUsageEvent(w, r)
		return
	}

	// GET /v1/mount-sources — list mountable source namespaces.
	if r.URL.Path == "/v1/mount-sources" && r.Method == http.MethodGet {
		if lister, ok := h.adapter.(store.MountSourceLister); ok {
			sources, err := lister.MountSources(r.Context())
			if err != nil {
				writeJSONError(w, err)
				return
			}
			writeJSON(w, map[string]any{"sources": sources})
			return
		}
		writeJSON(w, map[string]any{"sources": []any{}})
		return
	}

	// Docset routes
	if h.docsetMgr != nil && strings.HasPrefix(r.URL.Path, "/v1/docsets") {
		h.handleDocsets(w, r)
		return
	}

	source, op, ok := parseSourceRequest(r.URL)
	if !ok {
		http.NotFound(w, r)
		return
	}

	var resp any
	var err error

	// Cross-repo write gate: if X-Client-Repo indicates a different source,
	// verify the target repo has writable=true.
	clientRepo := r.Header.Get("X-Client-Repo")
	if source.Kind == store.SourceKindRepo && clientRepo != "" && clientRepo != source.Name {
		if r.Method == http.MethodPut || r.Method == http.MethodDelete {
			writable, err := h.repoWritable(r.Context(), source.Name)
			if err != nil {
				writeJSONError(w, err)
				return
			}
			if !writable {
				writeJSONErrorCode(w, http.StatusForbidden, "FORBIDDEN", "target repo does not allow cross-repo writes")
				return
			}
		}
	}

	if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		writeJSONErrorCode(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	adapter, err := h.adapterForSource(r.Context(), source)
	if err != nil {
		writeJSONError(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		resp, err = h.dispatchRead(r, adapter, source.Name, op)
	case http.MethodPut:
		resp, err = h.dispatchPut(r, adapter, source.Name, op)
	case http.MethodDelete:
		resp, err = h.dispatchDelete(r, adapter, source.Name, op)
	}

	if err != nil {
		writeJSONError(w, err)
		return
	}

	// ETag support for Cat: set ETag header and handle If-None-Match → 304.
	if cat, ok := resp.(*store.CatResponse); ok && cat.Hash != "" {
		etag := `"` + cat.Hash + `"`
		w.Header().Set("ETag", etag)
		if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch != "" {
			if etagMatch(ifNoneMatch, cat.Hash) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	writeJSON(w, resp)
}

func (h *handler) handleListRepos(w http.ResponseWriter, r *http.Request) {
	if catalog, ok := h.adapter.(interface {
		ListRepos(context.Context) ([]store.RepoInfo, error)
	}); ok {
		repos, err := catalog.ListRepos(r.Context())
		if err != nil {
			writeJSONError(w, err)
			return
		}
		writeJSON(w, map[string]any{"repos": repos})
		return
	}

	if reposer, ok := h.adapter.(store.Reposer); ok {
		names := reposer.Repos()
		repos := make([]map[string]string, len(names))
		for i, name := range names {
			repos[i] = map[string]string{"name": name}
		}
		writeJSON(w, map[string]any{"repos": repos})
		return
	}
	writeJSON(w, map[string]any{"repos": []any{}})
}

func (h *handler) handleRegisterRepo(w http.ResponseWriter, r *http.Request) {
	registrar, ok := h.adapter.(interface {
		RegisterRepo(context.Context, store.RegisterRepoRequest) (*store.RegisterRepoResponse, error)
	})
	if !ok {
		writeJSONErrorCode(w, http.StatusNotImplemented, "NOT_SUPPORTED", "repo registration is not supported by this backend")
		return
	}

	var req store.RegisterRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSONErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
		return
	}

	resp, err := registrar.RegisterRepo(r.Context(), req)
	if err != nil {
		writeJSONError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, resp)
}

func (h *handler) handleUsageEvent(w http.ResponseWriter, r *http.Request) {
	recorder, ok := h.adapter.(store.UsageRecorder)
	if !ok {
		writeJSONErrorCode(w, http.StatusNotImplemented, "NOT_SUPPORTED", "usage event recording is not supported by this backend")
		return
	}

	var req store.UsageEvent
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		writeJSONErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if req.LogID == "" {
		req.LogID = r.Header.Get("X-Gxfs-Log-Id")
	}
	if req.EventKind == "" {
		req.EventKind = store.UsageEventKindCLICommand
	}

	resp, err := recorder.RecordUsageEvent(r.Context(), req)
	if err != nil {
		writeJSONError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, resp)
}

func (h *handler) repoWritable(ctx context.Context, repo string) (bool, error) {
	if checker, ok := h.adapter.(repoWritableRefreshChecker); ok {
		return checker.RepoWritableWithRefresh(ctx, repo)
	}
	if checker, ok := h.adapter.(repoWritableChecker); ok {
		return checker.RepoWritable(repo), nil
	}
	return h.writableRepos[repo], nil
}

func (h *handler) adapterForSource(ctx context.Context, source store.SourceRef) (store.Adapter, error) {
	if source.Kind == store.SourceKindRepo {
		if router, ok := h.adapter.(store.SourceRouter); ok {
			return router.AdapterForSource(ctx, source)
		}
		return h.adapter, nil
	}

	router, ok := h.adapter.(store.SourceRouter)
	if !ok {
		return nil, fmt.Errorf("%w: %s", store.ErrNotSupported, source.String())
	}
	return router.AdapterForSource(ctx, source)
}

func (h *handler) dispatchRead(r *http.Request, adapter store.Adapter, repo, op string) (any, error) {
	q := r.URL.Query()
	switch op {
	case "ls":
		limit, err := queryIntNonNeg(q, "limit")
		if err != nil {
			return nil, err
		}
		offset, err := queryIntNonNeg(q, "offset")
		if err != nil {
			return nil, err
		}
		return adapter.LS(r.Context(), store.LSRequest{
			Repo:      repo,
			Path:      queryPath(q),
			Sort:      q.Get("sort"),
			Reverse:   queryBoolOr(q, "reverse"),
			Recursive: queryBoolOr(q, "recursive"),
			All:       queryBoolOr(q, "all"),
			Limit:     limit,
			Offset:    offset,
		})
	case "tree":
		depth, err := queryInt(q, "depth")
		if err != nil {
			return nil, err
		}
		return adapter.Tree(r.Context(), store.TreeRequest{
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
		return adapter.Cat(r.Context(), store.CatRequest{Repo: repo, Path: queryPath(q)})
	case "grep":
		regex, err := queryBool(q, "regex")
		if err != nil {
			return nil, err
		}
		ctxBefore, _ := queryInt(q, "context_before")
		ctxAfter, _ := queryInt(q, "context_after")
		return adapter.Grep(r.Context(), store.GrepRequest{
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
		limit, err := queryIntNonNeg(q, "limit")
		if err != nil {
			return nil, err
		}
		offset, err := queryIntNonNeg(q, "offset")
		if err != nil {
			return nil, err
		}
		return adapter.Find(r.Context(), store.FindRequest{
			Repo:     repo,
			Path:     queryPath(q),
			Name:     q.Get("name"),
			Type:     q.Get("type"),
			MaxDepth: maxDepth,
			MinDepth: minDepth,
			All:      queryBoolOr(q, "all"),
			IName:    q.Get("iname"),
			Limit:    limit,
			Offset:   offset,
		})
	case "hashes":
		return adapter.BatchHashes(r.Context(), store.HashRequest{Repo: repo, Path: queryPath(q)})
	case "stat":
		return adapter.Stat(r.Context(), store.StatRequest{Repo: repo, Path: queryPath(q)})
	case "search":
		limit, err := queryIntNonNeg(q, "limit")
		if err != nil {
			return nil, err
		}
		offset, err := queryIntNonNeg(q, "offset")
		if err != nil {
			return nil, err
		}
		return adapter.Search(r.Context(), store.SearchRequest{
			Repo:   repo,
			Query:  q.Get("q"),
			Path:   queryPath(q),
			Limit:  limit,
			Offset: offset,
		})
	case "glob":
		globber, ok := adapter.(store.Globber)
		if !ok {
			return nil, fmt.Errorf("glob is not supported by this backend")
		}
		pattern := q.Get("pattern")
		if pattern == "" {
			return nil, fmt.Errorf("%w: pattern is required", store.ErrInvalidParam)
		}
		globLimit, err := queryIntNonNeg(q, "limit")
		if err != nil {
			return nil, err
		}
		globOffset, err := queryIntNonNeg(q, "offset")
		if err != nil {
			return nil, err
		}
		return globber.Glob(r.Context(), store.GlobRequest{
			Repo:    repo,
			Pattern: pattern,
			Limit:   globLimit,
			Offset:  globOffset,
		})
	case "locate":
		locator, ok := adapter.(store.Locator)
		if !ok {
			return nil, fmt.Errorf("locate is not supported by this backend")
		}
		locateLimit, err := queryIntNonNeg(q, "limit")
		if err != nil {
			return nil, err
		}
		return locator.Locate(r.Context(), store.LocateRequest{
			Repo:  repo,
			Query: q.Get("q"),
			Limit: locateLimit,
		})
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}

func (h *handler) dispatchPut(r *http.Request, adapter store.Adapter, repo, op string) (any, error) {
	q := r.URL.Query()
	switch op {
	case "write":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		return adapter.Put(r.Context(), store.PutRequest{
			Repo:         repo,
			Path:         queryPath(q),
			Content:      string(body),
			ExpectedHash: expectedHash(r, q),
		})
	case "edit":
		var editReq struct {
			Old string `json:"old"`
			New string `json:"new"`
			All bool   `json:"all"`
		}
		if err := json.NewDecoder(r.Body).Decode(&editReq); err != nil {
			return nil, fmt.Errorf("decode edit body: %w", err)
		}
		return adapter.Edit(r.Context(), store.EditRequest{
			Repo:         repo,
			Path:         queryPath(q),
			Old:          editReq.Old,
			New:          editReq.New,
			All:          editReq.All,
			ExpectedHash: expectedHash(r, q),
		})
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}

func (h *handler) dispatchDelete(r *http.Request, adapter store.Adapter, repo, op string) (any, error) {
	q := r.URL.Query()
	switch op {
	case "delete":
		return adapter.Delete(r.Context(), store.DeleteRequest{
			Repo:         repo,
			Path:         queryPath(q),
			ExpectedHash: expectedHash(r, q),
		})
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}

// handleDocsets routes docset API requests.
func (h *handler) handleDocsets(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// POST /v1/docsets — create docset
	if path == "/v1/docsets" && r.Method == http.MethodPost {
		var req store.CreateDocsetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
			return
		}
		resp, err := h.docsetMgr.CreateDocset(r.Context(), req)
		if err != nil {
			writeJSONError(w, err)
			return
		}
		writeJSON(w, resp)
		return
	}

	// GET /v1/docsets — list docsets
	if path == "/v1/docsets" && r.Method == http.MethodGet {
		resp, err := h.docsetMgr.ListDocsets(r.Context())
		if err != nil {
			writeJSONError(w, err)
			return
		}
		writeJSON(w, resp)
		return
	}

	// GET /v1/docsets/{name} — get docset
	// DELETE /v1/docsets/{name} — delete docset
	if strings.HasPrefix(path, "/v1/docsets/") && strings.Count(path, "/") == 3 {
		name, err := url.PathUnescape(strings.TrimPrefix(path, "/v1/docsets/"))
		if err != nil || name == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			resp, err := h.docsetMgr.GetDocset(r.Context(), name)
			if err != nil {
				writeJSONError(w, err)
				return
			}
			writeJSON(w, resp)
			return
		case http.MethodDelete:
			if err := h.docsetMgr.DeleteDocset(r.Context(), name); err != nil {
				writeJSONError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			writeJSONErrorCode(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
	}

	// PUT /v1/docsets/{name}/members — add member
	// DELETE /v1/docsets/{name}/members — remove member
	if strings.HasPrefix(path, "/v1/docsets/") && strings.HasSuffix(path, "/members") {
		namePath := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/docsets/"), "/members")
		name, err := url.PathUnescape(namePath)
		if err != nil || name == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodPut:
			var req store.AddDocsetMemberRequest
			req.Name = name
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
				return
			}
			resp, err := h.docsetMgr.AddDocsetMember(r.Context(), req)
			if err != nil {
				writeJSONError(w, err)
				return
			}
			writeJSON(w, resp)
			return
		case http.MethodDelete:
			memberPath := r.URL.Query().Get("path")
			if memberPath == "" {
				writeJSONErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "path is required")
				return
			}
			if err := h.docsetMgr.RemoveDocsetMember(r.Context(), store.RemoveDocsetMemberRequest{
				Name: name,
				Path: memberPath,
			}); err != nil {
				writeJSONError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			writeJSONErrorCode(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
	}

	// GET /v1/docsets/{name}/docs?path=/... — read member content
	if strings.HasPrefix(path, "/v1/docsets/") && strings.HasSuffix(path, "/docs") {
		parts := strings.SplitN(strings.TrimPrefix(path, "/v1/docsets/"), "/docs", 2)
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		name, err := url.PathUnescape(parts[0])
		if err != nil || name == "" {
			http.NotFound(w, r)
			return
		}
		memberPath := queryPath(r.URL.Query())

		if r.Method != http.MethodGet {
			writeJSONErrorCode(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}

		resp, err := h.docsetMgr.GetDocsetMemberContent(r.Context(), store.GetDocsetMemberContentRequest{
			Name: name,
			Path: memberPath,
		})
		if err != nil {
			writeJSONError(w, err)
			return
		}
		writeJSON(w, resp)
		return
	}

	http.NotFound(w, r)
}

func parseSourceRequest(u *url.URL) (source store.SourceRef, op string, ok bool) {
	p := requestPath(u)
	for _, candidate := range []struct {
		prefix string
		param  string
		kind   store.SourceKind
	}{
		{prefix: "/v1/repos/", param: "repo", kind: store.SourceKindRepo},
		{prefix: "/v1/docs/", param: "name", kind: store.SourceKindDocs},
	} {
		rest := strings.TrimPrefix(p, candidate.prefix)
		if rest == p || rest == "" || strings.Contains(rest, "/") {
			continue
		}
		name := u.Query().Get(candidate.param)
		if name == "" {
			continue
		}
		return store.SourceRef{Kind: candidate.kind, Name: name}, rest, true
	}
	return store.SourceRef{}, "", false
}

func queryPath(q url.Values) string {
	if p := q.Get("path"); p != "" {
		return p
	}
	return "/"
}

// queryIntNonNeg parses a non-negative integer query parameter.
func queryIntNonNeg(q url.Values, key string) (int, error) {
	raw := q.Get(key)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid %s", store.ErrInvalidParam, key)
	}
	if value < 0 {
		return 0, fmt.Errorf("%w: %s must be non-negative", store.ErrInvalidParam, key)
	}
	return value, nil
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

func writeJSONStatus(w http.ResponseWriter, status int, resp any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeJSONError(w http.ResponseWriter, err error) {
	status, code := mapError(err)
	writeJSONErrorCode(w, status, code, err.Error())
}

// etagMatch checks whether the If-None-Match header value matches the given
// content hash. Supports both quoted ("sha256:...") and unquoted forms,
// as well as comma-separated multiple ETags per RFC 7232.
func etagMatch(ifNoneMatch, hash string) bool {
	quoted := `"` + hash + `"`
	for _, tag := range strings.Split(ifNoneMatch, ",") {
		t := strings.TrimSpace(tag)
		if t == hash || t == quoted {
			return true
		}
	}
	return false
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

// expectedHash returns the CAS hash from If-Match header or expected_hash query param.
// If-None-Match: * maps to "*" (create-only).
func expectedHash(r *http.Request, q url.Values) string {
	if ifMatch := r.Header.Get("If-Match"); ifMatch != "" {
		return strings.Trim(ifMatch, `"`)
	}
	if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch == "*" {
		return "*"
	}
	return q.Get("expected_hash")
}
func mapError(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrOldNotFound):
		return http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, store.ErrUnknownRepo):
		return http.StatusNotFound, "UNKNOWN_REPO"
	case errors.Is(err, store.ErrRepoExists):
		return http.StatusConflict, "REPO_EXISTS"
	case errors.Is(err, store.ErrUnknownSource):
		return http.StatusNotFound, "UNKNOWN_SOURCE"
	case errors.Is(err, store.ErrDocsetNotFound):
		return http.StatusNotFound, "DOCSET_NOT_FOUND"
	case errors.Is(err, store.ErrReadOnlyMount):
		return http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, store.ErrIsDir), errors.Is(err, store.ErrNotDir),
		errors.Is(err, store.ErrEmptyOld), errors.Is(err, store.ErrCannotDeleteRoot),
		errors.Is(err, store.ErrEmptyQuery),
		errors.Is(err, store.ErrInvalidParam),
		errors.Is(err, store.ErrInvalidName):
		return http.StatusBadRequest, "BAD_REQUEST"
	case errors.Is(err, store.ErrContentNotReady):
		return http.StatusNotFound, "CONTENT_NOT_READY"
	case errors.Is(err, store.ErrNotSupported):
		return http.StatusNotImplemented, "NOT_SUPPORTED"
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, "CONFLICT"
	case errors.Is(err, store.ErrDocsetNameExists):
		return http.StatusConflict, "DOCSET_NAME_EXISTS"
	case errors.Is(err, store.ErrDocsetMemberExists):
		return http.StatusConflict, "DOCSET_MEMBER_EXISTS"
	case errors.Is(err, store.ErrDocAlreadyInDocset):
		return http.StatusConflict, "DOC_ALREADY_IN_DOCSET"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}

// loggingMiddleware wraps an http.Handler and logs structured request info.
type loggingMiddleware struct {
	next http.Handler
}

func (lm *loggingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
	lm.next.ServeHTTP(rw, r)

	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"status", rw.status,
		"duration_ms", time.Since(start).Milliseconds(),
	}

	if logID := r.Header.Get("X-Gxfs-Log-Id"); logID != "" {
		attrs = append(attrs, "log_id", logID)
	}

	// Enhanced logging for cross-repo write operations.
	if clientRepo := r.Header.Get("X-Client-Repo"); clientRepo != "" {
		attrs = append(attrs,
			"client_repo", clientRepo,
			"mount_path", r.Header.Get("X-Mount-Path"),
		)
		source, op, _ := parseSourceRequest(r.URL)
		if op != "" {
			if source.Kind == store.SourceKindRepo {
				attrs = append(attrs, "target_repo", source.Name)
			} else {
				attrs = append(attrs, "target_source_kind", source.Kind, "target_source_name", source.Name)
			}
			attrs = append(attrs, "op", op)
		}
		result := "success"
		if rw.status >= 400 {
			result = "error"
		}
		if rw.status == http.StatusConflict {
			result = "conflict"
		}
		if rw.status == http.StatusForbidden {
			result = "rejected"
		}
		attrs = append(attrs, "result", result)
	}

	slog.Info("request", attrs...)
}

func requestPath(u *url.URL) string {
	if p := u.EscapedPath(); p != "" {
		return p
	}
	return u.Path
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
