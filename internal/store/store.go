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
	ErrEmptyQuery       = errors.New("search query cannot be empty")
	ErrInvalidParam     = errors.New("invalid parameter")
	ErrNotModified      = errors.New("not modified")
	ErrConflict         = errors.New("conflict: content hash mismatch")
	ErrNotSupported     = errors.New("operation not supported")
	ErrInvalidName      = errors.New("invalid name: must be lowercase alphanumeric with - or _")
	ErrNameExists       = errors.New("collection name already exists")
	ErrCollectionNotFound = errors.New("collection not found")
	ErrMemberExists     = errors.New("member path already exists in collection")
	ErrDocAlreadyInCollection = errors.New("document already in collection")
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

// === Collection Types ===

// Collection represents a curated set of documents across repos.
type Collection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// CollectionMember represents a document reference in a collection.
type CollectionMember struct {
	Path  string `json:"path"`   // path within the collection (e.g., "/guide.md")
	DocID string `json:"doc_id"` // reference to gxfs_docs.id
}

// CreateCollectionRequest is the input for creating a collection.
type CreateCollectionRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateCollectionResponse is the output for creating a collection.
type CreateCollectionResponse struct {
	Collection Collection `json:"collection"`
}

// ListCollectionsResponse is the output for listing collections.
type ListCollectionsResponse struct {
	Collections []Collection `json:"collections"`
}

// GetCollectionResponse is the output for getting a collection.
type GetCollectionResponse struct {
	Collection Collection         `json:"collection"`
	Members    []CollectionMember `json:"members"`
}

// AddMemberRequest is the input for adding a document to a collection.
type AddMemberRequest struct {
	Name       string `json:"name"`        // collection name
	SourceRef  string `json:"source_ref"`  // repo://repo-name/path
	Path       string `json:"path"`        // path within collection
}

// AddMemberResponse is the output for adding a member.
type AddMemberResponse struct {
	Member CollectionMember `json:"member"`
}

// RemoveMemberRequest is the input for removing a document from a collection.
type RemoveMemberRequest struct {
	Name string `json:"name"` // collection name
	Path string `json:"path"` // path within collection
}

// GetMemberContentRequest is the input for reading a collection member's content.
type GetMemberContentRequest struct {
	Name string `json:"name"` // collection name
	Path string `json:"path"` // path within collection
}

// GetMemberContentResponse is the output for reading a collection member's content.
type GetMemberContentResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Hash    string `json:"hash"`
}

// CollectionManager provides collection CRUD and membership operations.
type CollectionManager interface {
	CreateCollection(ctx context.Context, req CreateCollectionRequest) (*CreateCollectionResponse, error)
	ListCollections(ctx context.Context) (*ListCollectionsResponse, error)
	GetCollection(ctx context.Context, name string) (*GetCollectionResponse, error)
	DeleteCollection(ctx context.Context, name string) error
	AddMember(ctx context.Context, req AddMemberRequest) (*AddMemberResponse, error)
	RemoveMember(ctx context.Context, req RemoveMemberRequest) error
	GetMemberContent(ctx context.Context, req GetMemberContentRequest) (*GetMemberContentResponse, error)
}
