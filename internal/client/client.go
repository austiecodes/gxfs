package client

import (
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
	baseURL string
	http    *http.Client
}

var _ store.Adapter = (*Client)(nil)

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    http.DefaultClient,
	}
}

func (c *Client) LS(ctx context.Context, req store.LSRequest) (*store.LSResponse, error) {
	var resp store.LSResponse
	q := url.Values{"path": {req.Path}}
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
	if err := c.get(ctx, req.Repo, "tree", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Cat(ctx context.Context, req store.CatRequest) (*store.CatResponse, error) {
	var resp store.CatResponse
	q := url.Values{"path": {req.Path}}
	if err := c.get(ctx, req.Repo, "cat", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
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
	if err := c.get(ctx, req.Repo, "grep", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) Find(ctx context.Context, req store.FindRequest) (*store.FindResponse, error) {
	var resp store.FindResponse
	q := url.Values{
		"path": {req.Path},
		"name": {req.Name},
	}
	if err := c.get(ctx, req.Repo, "find", q, &resp); err != nil {
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

func (c *Client) get(ctx context.Context, repo, op string, q url.Values, out any) error {
	endpoint, err := c.url(repo, op, q)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build %s request: %w", op, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", op, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s failed: status %d: %s", op, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", op, err)
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
