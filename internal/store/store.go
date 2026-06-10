package store

import (
	"context"
	"errors"
)

var (
	ErrNotFound           = errors.New("path not found")
	ErrIsDir              = errors.New("is a directory")
	ErrNotDir             = errors.New("not a directory")
	ErrContentNotReady    = errors.New("content not loaded")
	ErrEmptyOld           = errors.New("old string cannot be empty")
	ErrOldNotFound        = errors.New("old string not found")
	ErrReadOnlyMount      = errors.New("read-only mount")
	ErrCannotDeleteRoot   = errors.New("cannot delete root")
	ErrUnknownRepo        = errors.New("unknown repo")
	ErrRepoExists         = errors.New("repo already exists")
	ErrUnknownSource      = errors.New("unknown source")
	ErrEmptyQuery         = errors.New("search query cannot be empty")
	ErrInvalidParam       = errors.New("invalid parameter")
	ErrNotModified        = errors.New("not modified")
	ErrConflict           = errors.New("conflict: content hash mismatch")
	ErrNotSupported       = errors.New("operation not supported")
	ErrInvalidName        = errors.New("invalid name: must be lowercase alphanumeric with - or _")
	ErrDocsetNameExists   = errors.New("docset name already exists")
	ErrDocsetNotFound     = errors.New("docset not found")
	ErrDocsetMemberExists = errors.New("member path already exists in docset")
	ErrDocAlreadyInDocset = errors.New("document already in docset")
)

type Node struct {
	Path    string            `json:"path"`
	Name    string            `json:"name"`
	Kind    string            `json:"kind"`
	Size    int64             `json:"size,omitempty"`
	ModTime string            `json:"mod_time,omitempty"`
	Hash    string            `json:"hash,omitempty"` // Deferred: not populated by Stat yet; see Phase 3E
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
	Limit     int  // max results (0 = unlimited)
	Offset    int  // skip first N results
}

type LSResponse struct {
	Nodes []Node `json:"nodes"`
	Total int    `json:"total,omitempty"` // total matching (pre-limit/offset)
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
	Repo        string
	Path        string
	IfNoneMatch string // optional: known hash; server returns 304 if content unchanged
}

type CatResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Hash    string `json:"hash,omitempty"`
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
	Limit    int    // max results (0 = unlimited)
	Offset   int    // skip first N results
}

type FindResponse struct {
	Nodes []Node `json:"nodes"`
	Total int    `json:"total,omitempty"` // total matching (pre-limit/offset)
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
	Repo         string
	Path         string
	Content      string
	ExpectedHash string // CAS: if set, reject update unless current hash matches; "*" means create-only (reject if exists)
}

type PutResponse struct {
	Node Node `json:"node"`
}

type DeleteRequest struct {
	Repo         string
	Path         string
	ExpectedHash string // CAS: if set, reject delete unless current hash matches
}

type DeleteResponse struct{}

type EditRequest struct {
	Repo         string
	Path         string
	Old          string
	New          string
	All          bool
	ExpectedHash string // CAS: if set, reject edit unless current hash matches
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
	Searcher
	BatchHasher
	Globber
	Locator
}

type Writer interface {
	Put(context.Context, PutRequest) (*PutResponse, error)
	Delete(context.Context, DeleteRequest) (*DeleteResponse, error)
}

type Editor interface {
	Edit(context.Context, EditRequest) (*EditResponse, error)
}

type SearchRequest struct {
	Repo   string
	Query  string
	Path   string // scope to this path prefix, empty = whole repo
	Limit  int    // max results, default 20
	Offset int    // skip first N results
}

type SearchResult struct {
	Path    string  `json:"path"`
	Rank    float64 `json:"rank"`
	Snippet string  `json:"snippet"`
	Size    int64   `json:"size"`
	ModTime string  `json:"mod_time,omitempty"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
}

type Searcher interface {
	Search(ctx context.Context, req SearchRequest) (*SearchResponse, error)
}

// LocateRequest is the input for Locate — lexical discovery with ranking.
type LocateRequest struct {
	Repo  string
	Query string
	Limit int // max results, default 10
}

// LocateResult is a single document match returned by Locate.
type LocateResult struct {
	Ref     string  `json:"ref"`     // repo://repo-name/path format
	Path    string  `json:"path"`    // original path within repo
	Score   float64 `json:"score"`   // lexical ranking score
	Snippet string  `json:"snippet"` // context snippet with highlights
}

// LocateResponse is the output for Locate.
type LocateResponse struct {
	Results []LocateResult `json:"results"`
	Total   int            `json:"total"`
}

// Locator provides lexical discovery with ranking, designed for cross-repo exploration.
type Locator interface {
	Locate(ctx context.Context, req LocateRequest) (*LocateResponse, error)
}

type CacheInvalidator interface {
	Invalidate()
}

// Reposer is an optional interface that adapters can implement to expose
// the list of available repositories. Registry implements this natively.
type Reposer interface {
	Repos() []string
}

// RepoInfo describes a repository namespace registered in the durable catalog.
type RepoInfo struct {
	Name      string `json:"name"`
	Writable  bool   `json:"writable"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// RegisterRepoRequest is the input for registering a repository namespace.
type RegisterRepoRequest struct {
	Name     string `json:"name"`
	Writable bool   `json:"writable"`
}

// RegisterRepoResponse is the output for registering a repository namespace.
type RegisterRepoResponse struct {
	Repo RepoInfo `json:"repo"`
}

// RepoRegistry manages repository namespaces in a durable registry.
type RepoRegistry interface {
	ListRepos(ctx context.Context) ([]RepoInfo, error)
	RegisterRepo(ctx context.Context, req RegisterRepoRequest) (*RegisterRepoResponse, error)
}

// DocNamespace describes a shared docs:// namespace.
type DocNamespace struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Writable    bool   `json:"writable"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Docset describes a curated docset:// namespace.
type Docset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// NamespaceCatalog lists durable source namespaces.
type NamespaceCatalog interface {
	ListDocNamespaces(ctx context.Context) ([]DocNamespace, error)
	ListDocsets(ctx context.Context) ([]Docset, error)
}

// MountSource describes a namespace that can be used as a mount source.
type MountSource struct {
	Ref         string     `json:"ref"`
	Kind        SourceKind `json:"kind"`
	Name        string     `json:"name"`
	Writable    bool       `json:"writable"`
	Description string     `json:"description,omitempty"`
}

// MountSourceLister exposes mountable source namespaces. Registry implements
// this for repo:// sources and future adapters can extend it with docs:// or
// docset:// sources.
type MountSourceLister interface {
	MountSources(ctx context.Context) ([]MountSource, error)
}

// SourceRouter resolves a typed source namespace to the adapter that owns it.
type SourceRouter interface {
	AdapterForSource(ctx context.Context, source SourceRef) (Adapter, error)
}

// GlobRequest is the input for Glob — path discovery via glob pattern.
type GlobRequest struct {
	Repo    string
	Pattern string // glob pattern like "**/*.md"
	Limit   int
	Offset  int
}

// GlobResult is a single path match returned by Glob.
type GlobResult struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time,omitempty"`
}

// GlobResponse is the output for Glob.
type GlobResponse struct {
	Results []GlobResult `json:"results"`
	Total   int          `json:"total"`
}

// Globber discovers file paths by glob pattern. This is a discovery
// operation (like search), not a mounted-view browsing operation.
type Globber interface {
	Glob(ctx context.Context, req GlobRequest) (*GlobResponse, error)
}

// DocsetMember represents a document reference in a docset.
type DocsetMember struct {
	Path  string `json:"path"`   // path within the docset (e.g., "/guide.md")
	DocID string `json:"doc_id"` // reference to rolio_docs.id
}

// CreateDocsetRequest is the input for creating a docset.
type CreateDocsetRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateDocsetResponse is the output for creating a docset.
type CreateDocsetResponse struct {
	Docset Docset `json:"docset"`
}

// ListDocsetsResponse is the output for listing docsets.
type ListDocsetsResponse struct {
	Docsets []Docset `json:"docsets"`
}

// GetDocsetResponse is the output for getting a docset.
type GetDocsetResponse struct {
	Docset  Docset         `json:"docset"`
	Members []DocsetMember `json:"members"`
}

// AddDocsetMemberRequest is the input for adding a document to a docset.
type AddDocsetMemberRequest struct {
	Name      string `json:"name"`       // docset name
	SourceRef string `json:"source_ref"` // repo://repo-name/path
	Path      string `json:"path"`       // path within docset
}

// AddDocsetMemberResponse is the output for adding a member.
type AddDocsetMemberResponse struct {
	Member DocsetMember `json:"member"`
}

// RemoveDocsetMemberRequest is the input for removing a document from a docset.
type RemoveDocsetMemberRequest struct {
	Name string `json:"name"` // docset name
	Path string `json:"path"` // path within docset
}

// GetDocsetMemberContentRequest is the input for reading a docset member's content.
type GetDocsetMemberContentRequest struct {
	Name string `json:"name"` // docset name
	Path string `json:"path"` // path within docset
}

// GetDocsetMemberContentResponse is the output for reading a docset member's content.
type GetDocsetMemberContentResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Hash    string `json:"hash"`
}

// DocsetManager provides docset CRUD and membership operations.
type DocsetManager interface {
	CreateDocset(ctx context.Context, req CreateDocsetRequest) (*CreateDocsetResponse, error)
	ListDocsets(ctx context.Context) (*ListDocsetsResponse, error)
	GetDocset(ctx context.Context, name string) (*GetDocsetResponse, error)
	DeleteDocset(ctx context.Context, name string) error
	AddDocsetMember(ctx context.Context, req AddDocsetMemberRequest) (*AddDocsetMemberResponse, error)
	RemoveDocsetMember(ctx context.Context, req RemoveDocsetMemberRequest) error
	GetDocsetMemberContent(ctx context.Context, req GetDocsetMemberContentRequest) (*GetDocsetMemberContentResponse, error)
}
