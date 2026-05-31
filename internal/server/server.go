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

// NewHandlerWithCollections creates a handler with collection support.
func NewHandlerWithCollections(adapter store.Adapter, writableRepos map[string]bool, collectionMgr store.CollectionManager) http.Handler {
	inv, _ := adapter.(store.CacheInvalidator)
	h := &handler{adapter: adapter, invalidator: inv, writableRepos: writableRepos, collectionMgr: collectionMgr}
	return &loggingMiddleware{next: h}
}

type handler struct {
	adapter       store.Adapter
	invalidator   store.CacheInvalidator
	writableRepos map[string]bool         // repos that accept cross-repo writes
	collectionMgr store.CollectionManager // optional: collection management
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

	// Collection routes
	if h.collectionMgr != nil && strings.HasPrefix(r.URL.Path, "/v1/collections") {
		h.handleCollections(w, r)
		return
	}

	source, op, ok := parseSourcePath(requestPath(r.URL))
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

// handleCollections routes collection API requests.
func (h *handler) handleCollections(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// POST /v1/collections — create collection
	if path == "/v1/collections" && r.Method == http.MethodPost {
		var req store.CreateCollectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
			return
		}
		resp, err := h.collectionMgr.CreateCollection(r.Context(), req)
		if err != nil {
			writeJSONError(w, err)
			return
		}
		writeJSON(w, resp)
		return
	}

	// GET /v1/collections — list collections
	if path == "/v1/collections" && r.Method == http.MethodGet {
		resp, err := h.collectionMgr.ListCollections(r.Context())
		if err != nil {
			writeJSONError(w, err)
			return
		}
		writeJSON(w, resp)
		return
	}

	// GET /v1/collections/{name} — get collection
	// DELETE /v1/collections/{name} — delete collection
	if strings.HasPrefix(path, "/v1/collections/") && strings.Count(path, "/") == 3 {
		name, err := url.PathUnescape(strings.TrimPrefix(path, "/v1/collections/"))
		if err != nil || name == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			resp, err := h.collectionMgr.GetCollection(r.Context(), name)
			if err != nil {
				writeJSONError(w, err)
				return
			}
			writeJSON(w, resp)
			return
		case http.MethodDelete:
			if err := h.collectionMgr.DeleteCollection(r.Context(), name); err != nil {
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

	// PUT /v1/collections/{name}/members — add member
	// DELETE /v1/collections/{name}/members — remove member
	if strings.HasPrefix(path, "/v1/collections/") && strings.HasSuffix(path, "/members") {
		namePath := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/collections/"), "/members")
		name, err := url.PathUnescape(namePath)
		if err != nil || name == "" {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodPut:
			var req store.AddMemberRequest
			req.Name = name
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONErrorCode(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
				return
			}
			resp, err := h.collectionMgr.AddMember(r.Context(), req)
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
			if err := h.collectionMgr.RemoveMember(r.Context(), store.RemoveMemberRequest{
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

	// GET /v1/collections/{name}/docs?path=/... — read member content
	if strings.HasPrefix(path, "/v1/collections/") && strings.HasSuffix(path, "/docs") {
		parts := strings.SplitN(strings.TrimPrefix(path, "/v1/collections/"), "/docs", 2)
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

		resp, err := h.collectionMgr.GetMemberContent(r.Context(), store.GetMemberContentRequest{
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

func parsePath(p string) (repo string, op string, ok bool) {
	source, op, ok := parseSourcePath(p)
	if !ok || source.Kind != store.SourceKindRepo {
		return "", "", false
	}
	return source.Name, op, true
}

func parseSourcePath(p string) (source store.SourceRef, op string, ok bool) {
	prefixes := []struct {
		prefix string
		kind   store.SourceKind
	}{
		{prefix: "/v1/repos/", kind: store.SourceKindRepo},
		{prefix: "/v1/docs/", kind: store.SourceKindDocs},
	}
	for _, candidate := range prefixes {
		rest := strings.TrimPrefix(p, candidate.prefix)
		if rest == p {
			continue
		}
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return store.SourceRef{}, "", false
		}
		name, err := url.PathUnescape(parts[0])
		if err != nil {
			return store.SourceRef{}, "", false
		}
		return store.SourceRef{Kind: candidate.kind, Name: name}, parts[1], true
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
	case errors.Is(err, store.ErrCollectionNotFound):
		return http.StatusNotFound, "COLLECTION_NOT_FOUND"
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
	case errors.Is(err, store.ErrNameExists):
		return http.StatusConflict, "NAME_EXISTS"
	case errors.Is(err, store.ErrMemberExists):
		return http.StatusConflict, "MEMBER_EXISTS"
	case errors.Is(err, store.ErrDocAlreadyInCollection):
		return http.StatusConflict, "DOC_ALREADY_IN_COLLECTION"
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
		source, op, _ := parseSourcePath(requestPath(r.URL))
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
