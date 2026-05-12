package store

import (
	"context"
	"errors"
)

var (
	ErrNotFound         = errors.New("path not found")
	ErrIsDir            = errors.New("is a directory")
	ErrNotDir           = errors.New("not a directory")
	ErrContentNotReady  = errors.New("content not loaded")
	ErrEmptyOld         = errors.New("old string cannot be empty")
	ErrOldNotFound      = errors.New("old string not found")
	ErrReadOnlyMount    = errors.New("read-only mount")
	ErrCannotDeleteRoot = errors.New("cannot delete root")
	ErrUnknownRepo      = errors.New("unknown repo")
)

type Node struct {
	Path    string            `json:"path"`
	Name    string            `json:"name"`
	Kind    string            `json:"kind"`
	Size    int64             `json:"size,omitempty"`
	ModTime string            `json:"mod_time,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type Match struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Text   string   `json:"text"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

type LSRequest struct {
	Repo      string
	Path      string
	Sort      string // "name" (default), "size", "mtime"
	Reverse   bool
	Recursive bool
	All       bool // show hidden files (names starting with .)
}

type LSResponse struct {
	Nodes []Node `json:"nodes"`
}

type TreeRequest struct {
	Repo      string
	Path      string
	Depth     int
	All       bool
	DirsOnly  bool
	FullPath  bool
	ShowSize  bool
	Sort      string // "name" (default), "size", "mtime"
	DirsFirst bool
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
	Repo            string
	Path            string
	Pattern         string
	Regex           bool
	CaseInsensitive bool
	Invert          bool
	WholeWord       bool
	WholeLine       bool
	ContextBefore   int
	ContextAfter    int
	All             bool
	Include         string
	Exclude         string
}

type GrepResponse struct {
	Matches []Match `json:"matches"`
}

type FindRequest struct {
	Repo     string
	Path     string
	Name     string
	Type     string // "file" or "" = files only, "dir" = dirs only
	MaxDepth int
	MinDepth int
	All      bool   // include hidden files
	IName    string // case-insensitive name glob (empty = use Name only)
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

type PutRequest struct {
	Repo    string
	Path    string
	Content string
}

type PutResponse struct {
	Node Node `json:"node"`
}

type DeleteRequest struct {
	Repo string
	Path string
}

type DeleteResponse struct{}

type EditRequest struct {
	Repo string
	Path string
	Old  string
	New  string
	All  bool
}

type EditResponse struct {
	Path     string `json:"path"`
	Replaced int    `json:"replaced"`
	Content  string `json:"content"`
}

type Adapter interface {
	Lister
	Treer
	Catter
	Grepper
	Finder
	Statter
	Writer
	Editor
}

type Writer interface {
	Put(context.Context, PutRequest) (*PutResponse, error)
	Delete(context.Context, DeleteRequest) (*DeleteResponse, error)
}

type Editor interface {
	Edit(context.Context, EditRequest) (*EditResponse, error)
}

type CacheInvalidator interface {
	Invalidate()
}
