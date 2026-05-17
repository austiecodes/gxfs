package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gxfs/internal/store"
)

type Client struct {
	baseURL    string
	http       *http.Client
	clientRepo string // sent as X-Client-Repo header for cross-repo write gate
	mountPath  string // sent as X-Mount-Path header for observability
}

var _ store.Adapter = (*Client)(nil)

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    http.DefaultClient,
	}
}

// SetClientRepo sets the client repo name sent as X-Client-Repo header.
func (c *Client) SetClientRepo(repo string) {
	c.clientRepo = repo
}

// ClientRepo returns the configured client repo name.
func (c *Client) ClientRepo() string {
	return c.clientRepo
}

// SetMountPath sets the mount path sent as X-Mount-Path header.
func (c *Client) SetMountPath(mp string) {
	c.mountPath = mp
}

// RepoList returns the list of repository names available on the server.
func (c *Client) RepoList(ctx context.Context) ([]string, error) {
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/repos"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build repo_list request: %w", err)
	}
	var resp struct {
		Repos []struct {
			Name string `json:"name"`
		} `json:"repos"`
	}
	if err := c.do(req, "repo_list", &resp); err != nil {
		return nil, err
	}
	names := make([]string, len(resp.Repos))
	for i, r := range resp.Repos {
		names[i] = r.Name
	}
	return names, nil
}


func (c *Client) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	var resp store.LSResponse
	q := url.Values{"path": {req.Path}}
	if req.Sort != "" {
		q.Set("sort", req.Sort)
	}
	if req.Reverse {
		q.Set("reverse", "true")
	}
	if req.Recursive {
		q.Set("recursive", "true")
	}
	if req.All {
		q.Set("all", "true")
	}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Offset > 0 {
		q.Set("offset", strconv.Itoa(req.Offset))
	}
	if err := c.get(ctx, req.Repo, "ls", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Tree(ctx context.Context, req store.TreeRequest) (*store.TreeResponse, error) {
	var resp store.TreeResponse
	q := url.Values{"path": {req.Path}}
	if req.Depth > 0 {
		q.Set("depth", strconv.Itoa(req.Depth))
	}
	if req.All {
		q.Set("all", "true")
	}
	if req.DirsOnly {
		q.Set("dirs_only", "true")
	}
	if req.FullPath {
		q.Set("full_path", "true")
	}
	if req.ShowSize {
		q.Set("show_size", "true")
	}
	if req.Sort != "" {
		q.Set("sort", req.Sort)
	}
	if req.DirsFirst {
		q.Set("dirs_first", "true")
	}
	if err := c.get(ctx, req.Repo, "tree", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Cat(ctx context.Context, req store.CatRequest) (*store.CatResponse, error) {
	q := url.Values{"path": {req.Path}}
	endpoint, err := c.url(req.Repo, "cat", q)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build cat request: %w", err)
	}

	// Send If-None-Match header if caller provides a known hash.
	if req.IfNoneMatch != "" {
		httpReq.Header.Set("If-None-Match", `"`+req.IfNoneMatch+`"`)
	}

	// Use doWithAllowed so 304 is handled gracefully while preserving
	// the shared JSON error parsing for non-2xx responses.
	var catResp store.CatResponse
	err = c.doWithAllowed(httpReq, "cat", &catResp, []int{http.StatusNotModified})
	if err != nil {
		return nil, err
	}

	// doWithAllowed returns nil for allowed codes (304) without decoding.
	// Path is always populated on 200, zero-valued on undecoded 304.
	if catResp.Path == "" && req.IfNoneMatch != "" {
		return &store.CatResponse{Path: req.Path, Hash: req.IfNoneMatch}, store.ErrNotModified
	}
	return &catResp, nil
}

func (c *Client) Grep(ctx context.Context, req store.GrepRequest) (*store.GrepResponse, error) {
	var resp store.GrepResponse
	q := url.Values{
		"path":    {req.Path},
		"pattern": {req.Pattern},
	}
	if req.Regex {
		q.Set("regex", "true")
	}
	if req.CaseInsensitive {
		q.Set("case_insensitive", "true")
	}
	if req.Invert {
		q.Set("invert", "true")
	}
	if req.WholeWord {
		q.Set("whole_word", "true")
	}
	if req.WholeLine {
		q.Set("whole_line", "true")
	}
	if req.ContextBefore > 0 {
		q.Set("context_before", strconv.Itoa(req.ContextBefore))
	}
	if req.ContextAfter > 0 {
		q.Set("context_after", strconv.Itoa(req.ContextAfter))
	}
	if req.All {
		q.Set("all", "true")
	}
	if req.Include != "" {
		q.Set("include", req.Include)
	}
	if req.Exclude != "" {
		q.Set("exclude", req.Exclude)
	}
	if err := c.get(ctx, req.Repo, "grep", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	var resp store.FindResponse
	q := url.Values{"path": {req.Path}}
	if req.Name != "" {
		q.Set("name", req.Name)
	}
	if req.Type != "" {
		q.Set("type", req.Type)
	}
	if req.MaxDepth > 0 {
		q.Set("maxdepth", strconv.Itoa(req.MaxDepth))
	}
	if req.MinDepth > 0 {
		q.Set("mindepth", strconv.Itoa(req.MinDepth))
	}
	if req.All {
		q.Set("all", "true")
	}
	if req.IName != "" {
		q.Set("iname", req.IName)
	}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Offset > 0 {
		q.Set("offset", strconv.Itoa(req.Offset))
	}
	if err := c.get(ctx, req.Repo, "find", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) BatchHashes(ctx context.Context, req store.HashRequest) (*store.HashResponse, error) {
	var resp store.HashResponse
	q := url.Values{}
	if req.Path != "" {
		q.Set("path", req.Path)
	}
	if err := c.get(ctx, req.Repo, "hashes", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Stat(ctx context.Context, req store.StatRequest) (*store.StatResponse, error) {
	var resp store.StatResponse
	q := url.Values{"path": {req.Path}}
	if err := c.get(ctx, req.Repo, "stat", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Put(ctx context.Context, req store.PutRequest) (*store.PutResponse, error) {
	var resp store.PutResponse
	q := url.Values{"path": {req.Path}}
	if req.ExpectedHash != "" {
		q.Set("expected_hash", req.ExpectedHash)
	}
	if err := c.putWithHeaders(ctx, req.Repo, "write", q, req.Content, &resp, req.ExpectedHash); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Delete(ctx context.Context, req store.DeleteRequest) (*store.DeleteResponse, error) {
	q := url.Values{"path": {req.Path}}
	if req.ExpectedHash != "" {
		q.Set("expected_hash", req.ExpectedHash)
	}
	if err := c.deleteWithHeaders(ctx, req.Repo, q, req.ExpectedHash != ""); err != nil {
		return nil, err
	}
	return &store.DeleteResponse{}, nil
}

func (c *Client) Edit(ctx context.Context, req store.EditRequest) (*store.EditResponse, error) {
	var resp store.EditResponse
	q := url.Values{"path": {req.Path}}
	if req.ExpectedHash != "" {
		q.Set("expected_hash", req.ExpectedHash)
	}
	body, _ := json.Marshal(map[string]any{"old": req.Old, "new": req.New, "all": req.All})
	if err := c.putJSONWithHeaders(ctx, req.Repo, "edit", q, body, &resp, req.ExpectedHash != ""); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Search(ctx context.Context, req store.SearchRequest) (*store.SearchResponse, error) {
	var resp store.SearchResponse
	q := url.Values{"q": {req.Query}}
	if req.Path != "" {
		q.Set("path", req.Path)
	}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Offset > 0 {
		q.Set("offset", strconv.Itoa(req.Offset))
	}
	if err := c.get(ctx, req.Repo, "search", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Locate(ctx context.Context, req store.LocateRequest) (*store.LocateResponse, error) {
	var resp store.LocateResponse
	q := url.Values{"q": {req.Query}}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if err := c.get(ctx, req.Repo, "locate", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Glob(ctx context.Context, req store.GlobRequest) (*store.GlobResponse, error) {
	var resp store.GlobResponse
	q := url.Values{"pattern": {req.Pattern}}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Offset > 0 {
		q.Set("offset", strconv.Itoa(req.Offset))
	}
	if err := c.get(ctx, req.Repo, "glob", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) get(ctx context.Context, repo, op string, q url.Values, out any) error {
	endpoint, err := c.url(repo, op, q)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build %s request: %w", op, err)
	}
	return c.do(req, op, out)
}

func (c *Client) put(ctx context.Context, repo, op string, q url.Values, body string, out any) error {
	endpoint, err := c.url(repo, op, q)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s request: %w", op, err)
	}
	req.Header.Set("Content-Type", "text/plain")
	return c.do(req, op, out)
}

func (c *Client) putJSON(ctx context.Context, repo, op string, q url.Values, body []byte, out any) error {
	endpoint, err := c.url(repo, op, q)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s request: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, op, out)
}

func (c *Client) delete(ctx context.Context, repo string, q url.Values) error {
	endpoint, err := c.url(repo, "delete", q)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build delete request: %w", err)
	}
	return c.do(req, "delete", nil)
}

// putWithHeaders is like put but sets CAS and cross-repo headers.
func (c *Client) putWithHeaders(ctx context.Context, repo, op string, q url.Values, body string, out any, expectedHash string) error {
	endpoint, err := c.url(repo, op, q)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s request: %w", op, err)
	}
	req.Header.Set("Content-Type", "text/plain")
	c.setWriteHeaders(req, repo, expectedHash)
	return c.do(req, op, out)
}

// putJSONWithHeaders is like putJSON but sets CAS and cross-repo headers.
func (c *Client) putJSONWithHeaders(ctx context.Context, repo, op string, q url.Values, body []byte, out any, hasExpectedHash bool) error {
	endpoint, err := c.url(repo, op, q)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s request: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setWriteHeaders(req, repo, q.Get("expected_hash"))
	return c.do(req, op, out)
}

// deleteWithHeaders is like delete but sets CAS and cross-repo headers.
func (c *Client) deleteWithHeaders(ctx context.Context, repo string, q url.Values, hasExpectedHash bool) error {
	endpoint, err := c.url(repo, "delete", q)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build delete request: %w", err)
	}
	c.setWriteHeaders(req, repo, q.Get("expected_hash"))
	return c.do(req, "delete", nil)
}

// setWriteHeaders adds X-Client-Repo, X-Mount-Path and If-Match/If-None-Match headers.
func (c *Client) setWriteHeaders(req *http.Request, targetRepo, expectedHash string) {
	if c.clientRepo != "" && c.clientRepo != targetRepo {
		req.Header.Set("X-Client-Repo", c.clientRepo)
	}
	if c.mountPath != "" {
		req.Header.Set("X-Mount-Path", c.mountPath)
	}
	if expectedHash == "*" {
		req.Header.Set("If-None-Match", "*")
	} else if expectedHash != "" {
		req.Header.Set("If-Match", `"`+expectedHash+`"`)
	}
}

func (c *Client) do(req *http.Request, op string, out any) error {
	return c.doWithAllowed(req, op, out, nil)
}

// doWithAllowed is like do but accepts additional allowed status codes
// beyond the normal 2xx range. For allowed codes, no JSON decode is attempted
// and the caller handles the response.
func (c *Client) doWithAllowed(req *http.Request, op string, out any, allowed []int) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", op, err)
	}
	defer resp.Body.Close()

	// Check if this is an explicitly allowed non-2xx status (e.g. 304).
	for _, code := range allowed {
		if resp.StatusCode == code {
			return nil
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			err := fmt.Errorf("%s failed: status %d: %s", op, resp.StatusCode, errResp.Error.Message)
			if errResp.Error.Code == "NOT_FOUND" {
				return fmt.Errorf("%w: %w", store.ErrNotFound, err)
			}
			return err
		}
		return fmt.Errorf("%s failed: status %d: %s", op, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s response: %w", op, err)
		}
	}
	return nil
}

func (c *Client) url(repo, op string, q url.Values) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/v1/repos/" + url.PathEscape(repo) + "/" + op
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// === Collection Methods ===

// CreateCollection creates a new collection.
func (c *Client) CreateCollection(ctx context.Context, req store.CreateCollectionRequest) (*store.CreateCollectionResponse, error) {
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/collections"
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal create collection request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build create collection request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	var resp store.CreateCollectionResponse
	if err := c.do(httpReq, "create_collection", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListCollections lists all collections.
func (c *Client) ListCollections(ctx context.Context) (*store.ListCollectionsResponse, error) {
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/collections"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build list collections request: %w", err)
	}
	var resp store.ListCollectionsResponse
	if err := c.do(req, "list_collections", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetCollection gets a collection by name.
func (c *Client) GetCollection(ctx context.Context, name string) (*store.GetCollectionResponse, error) {
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/collections/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build get collection request: %w", err)
	}
	var resp store.GetCollectionResponse
	if err := c.do(req, "get_collection", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteCollection deletes a collection by name.
func (c *Client) DeleteCollection(ctx context.Context, name string) error {
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/collections/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build delete collection request: %w", err)
	}
	return c.do(req, "delete_collection", nil)
}

// AddMember adds a document to a collection.
func (c *Client) AddMember(ctx context.Context, req store.AddMemberRequest) (*store.AddMemberResponse, error) {
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/collections/" + url.PathEscape(req.Name) + "/members"
	body, err := json.Marshal(map[string]string{
		"source_ref": req.SourceRef,
		"path":       req.Path,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal add member request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build add member request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	var resp store.AddMemberResponse
	if err := c.do(httpReq, "add_member", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RemoveMember removes a document from a collection.
func (c *Client) RemoveMember(ctx context.Context, name, path string) error {
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/collections/" + url.PathEscape(name) + "/members?path=" + url.QueryEscape(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build remove member request: %w", err)
	}
	return c.do(req, "remove_member", nil)
}

// GetMemberContent reads a document's content via collection membership.
func (c *Client) GetMemberContent(ctx context.Context, name, path string) (*store.GetMemberContentResponse, error) {
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/collections/" + url.PathEscape(name) + "/docs?path=" + url.QueryEscape(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build get member content request: %w", err)
	}
	var resp store.GetMemberContentResponse
	if err := c.do(req, "get_member_content", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
