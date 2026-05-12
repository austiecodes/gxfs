package syncmanifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Manifest struct {
	Version     int       `toml:"version"`
	GeneratedAt time.Time `toml:"generated_at"`
	Entries     []Entry   `toml:"entries"`
}

type Entry struct {
	Local        string `toml:"local"`
	RemoteDoc    string `toml:"remote_doc"`
	Mount        string `toml:"mount"`
	ContentHash  string `toml:"content_hash"`
	Size         int64  `toml:"size"`
	MTime        string `toml:"mtime"`
	Materialized bool   `toml:"materialized"`
}

func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}

	var manifest Manifest
	if err := toml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if manifest.Version == 0 {
		manifest.Version = 1
	}
	return manifest, nil
}

func Save(path string, manifest Manifest) error {
	if manifest.Version == 0 {
		manifest.Version = 1
	}
	if manifest.GeneratedAt.IsZero() {
		manifest.GeneratedAt = time.Now().UTC()
	}
	sort.Slice(manifest.Entries, func(i, j int) bool {
		return manifest.Entries[i].Local < manifest.Entries[j].Local
	})

	data, err := toml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	return nil
}

func Upsert(existing Manifest, entries []Entry) Manifest {
	if existing.Version == 0 {
		existing.Version = 1
	}
	byLocal := make(map[string]Entry, len(existing.Entries)+len(entries))
	for _, entry := range existing.Entries {
		byLocal[entry.Local] = entry
	}
	for _, entry := range entries {
		byLocal[entry.Local] = entry
	}

	existing.GeneratedAt = time.Now().UTC()
	existing.Entries = existing.Entries[:0]
	for _, entry := range byLocal {
		existing.Entries = append(existing.Entries, entry)
	}
	sort.Slice(existing.Entries, func(i, j int) bool {
		return existing.Entries[i].Local < existing.Entries[j].Local
	})
	return existing
}

func ReplaceUnder(existing Manifest, root string, entries []Entry) Manifest {
	if existing.Version == 0 {
		existing.Version = 1
	}

	root = cleanManifestPath(root)
	next := existing.Entries[:0]
	for _, entry := range existing.Entries {
		if !isUnder(entry.Local, root) {
			next = append(next, entry)
		}
	}
	existing.Entries = append(next, entries...)
	existing.GeneratedAt = time.Now().UTC()
	sort.Slice(existing.Entries, func(i, j int) bool {
		return existing.Entries[i].Local < existing.Entries[j].Local
	})
	return existing
}

func EntriesUnder(manifest Manifest, root string) []Entry {
	root = cleanManifestPath(root)
	var entries []Entry
	for _, entry := range manifest.Entries {
		if isUnder(entry.Local, root) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Local < entries[j].Local
	})
	return entries
}

func UpdateEntries(existing Manifest, updates []Entry) Manifest {
	if existing.Version == 0 {
		existing.Version = 1
	}
	byLocal := make(map[string]Entry, len(updates))
	for _, entry := range updates {
		byLocal[entry.Local] = entry
	}
	for i, entry := range existing.Entries {
		if updated, ok := byLocal[entry.Local]; ok {
			existing.Entries[i] = updated
		}
	}
	existing.GeneratedAt = time.Now().UTC()
	sort.Slice(existing.Entries, func(i, j int) bool {
		return existing.Entries[i].Local < existing.Entries[j].Local
	})
	return existing
}

func cleanManifestPath(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	if p == "." {
		return ""
	}
	return p
}

func isUnder(local, root string) bool {
	local = cleanManifestPath(local)
	root = cleanManifestPath(root)
	if root == "" {
		return true
	}
	return local == root || strings.HasPrefix(local, root+"/")
}
