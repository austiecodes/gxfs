package syncmanifest

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type LocalFile struct {
	LocalPath   string
	Content     string
	ContentHash string
	Size        int64
	MTime       time.Time
}

func ScanLocal(root string) ([]LocalFile, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", root, err)
	}
	if info.IsDir() {
		return scanDir(root)
	}

	file, err := readLocalFile(root, root)
	if err != nil {
		return nil, err
	}
	return []LocalFile{file}, nil
}

func scanDir(root string) ([]LocalFile, error) {
	var files []LocalFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		file, err := readLocalFile(root, path)
		if err != nil {
			return err
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].LocalPath < files[j].LocalPath
	})
	return files, nil
}

func readLocalFile(root, filePath string) (LocalFile, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return LocalFile{}, fmt.Errorf("stat %s: %w", filePath, err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return LocalFile{}, fmt.Errorf("read %s: %w", filePath, err)
	}

	localPath, err := localPath(root, filePath)
	if err != nil {
		return LocalFile{}, err
	}
	sum := sha256.Sum256(data)
	return LocalFile{
		LocalPath:   localPath,
		Content:     string(data),
		ContentHash: fmt.Sprintf("sha256:%x", sum),
		Size:        info.Size(),
		MTime:       info.ModTime().UTC(),
	}, nil
}

func localPath(root, filePath string) (string, error) {
	root = filepath.Clean(root)
	filePath = filepath.Clean(filePath)
	rootLocal, err := visibleLocalPath(root)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return rootLocal, nil
	}

	rel, err := filepath.Rel(root, filePath)
	if err != nil {
		return "", fmt.Errorf("make %s relative to %s: %w", filePath, root, err)
	}
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "./")
	if rel == "." {
		return rootLocal, nil
	}
	return path.Join(rootLocal, rel), nil
}

func visibleLocalPath(p string) (string, error) {
	p = filepath.Clean(p)
	if filepath.IsAbs(p) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get cwd: %w", err)
		}
		if rel, err := filepath.Rel(cwd, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			p = rel
		}
	}
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	return p, nil
}
