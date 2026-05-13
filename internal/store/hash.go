package store

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// HashContent returns the SHA-256 content hash in "sha256:<hex>" format.
// This is the canonical hash function used across the project for content
// deduplication, sync conflict detection, and ETag generation.
func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("sha256:%x", sum)
}

// ContentHash represents a single path-to-hash mapping returned by BatchHashes.
type ContentHash struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// HashRequest is the input for BatchHashes.
type HashRequest struct {
	Repo string
	Path string // scope to this path prefix, empty = whole repo
}

// HashResponse is the output for BatchHashes.
type HashResponse struct {
	Hashes []ContentHash `json:"hashes"`
}

// BatchHasher returns known content hashes for all files under a given repo path.
// Files without a computed hash (e.g. backfill pending) are simply omitted from
// the response; callers must treat missing paths as "hash unknown, must Cat".
type BatchHasher interface {
	BatchHashes(ctx context.Context, req HashRequest) (*HashResponse, error)
}
