package store

import "context"

type Node struct {
	Path    string            `json:"path"`
	Name    string            `json:"name"`
	Kind    string            `json:"kind"`
	Size    int64             `json:"size,omitempty"`
	ModTime string            `json:"mod_time,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type LSRequest struct {
	Repo string
	Path string
}

type LSResponse struct {
	Nodes []Node `json:"nodes"`
}

type TreeRequest struct {
	Repo  string
	Path  string
	Depth int
}

type TreeResponse struct {
	Root Node   `json:"root"`
	Text string `json:"text"`
}

type CatRequest struct {
	Repo string
	Path string
}

type CatResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type GrepRequest struct {
	Repo    string
	Path    string
	Pattern string
	Regex   bool
}

type GrepResponse struct {
	Matches []Match `json:"matches"`
}

type FindRequest struct {
	Repo string
	Path string
	Name string
}

type FindResponse struct {
	Nodes []Node `json:"nodes"`
}

type StatRequest struct {
	Repo string
	Path string
}

type StatResponse struct {
	Node Node `json:"node"`
}

type Lister interface {
	LS(context.Context, LSRequest) (*LSResponse, error)
}

type Treer interface {
	Tree(context.Context, TreeRequest) (*TreeResponse, error)
}

type Catter interface {
	Cat(context.Context, CatRequest) (*CatResponse, error)
}

type Grepper interface {
	Grep(context.Context, GrepRequest) (*GrepResponse, error)
}

type Finder interface {
	Find(context.Context, FindRequest) (*FindResponse, error)
}

type Statter interface {
	Stat(context.Context, StatRequest) (*StatResponse, error)
}

type Adapter interface {
	Lister
	Treer
	Catter
	Grepper
	Finder
	Statter
}
