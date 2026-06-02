package command

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/spf13/cobra"

	mountadapter "github.com/austiecodes/gxfs/internal/mount"
	"github.com/austiecodes/gxfs/internal/store"
	"github.com/austiecodes/gxfs/internal/syncmanifest"
)

func NewSyncCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize local docs with GXFS",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	syncCmd.AddCommand(newSyncPushCommand(adapter, repo, resolver))
	syncCmd.AddCommand(newSyncPullCommand(adapter, rawAdapter, repo, resolver))
	syncCmd.AddCommand(NewRefreshCommand(adapter, rawAdapter, repo, resolver))
	syncCmd.AddCommand(NewMaterializeCommand(adapter, rawAdapter, repo, resolver))
	syncCmd.AddCommand(NewDematerializeCommand())
	return syncCmd
}

func newSyncPushCommand(adapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var manifestPath string
	cmd := &cobra.Command{
		Use:   "push <local-path>",
		Short: "Push local docs into GXFS",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			files, err := syncmanifest.ScanLocal(root)
			if err != nil {
				return err
			}

			entries := make([]syncmanifest.Entry, 0, len(files))
			for _, file := range files {
				resp, err := adapter.Put(cmd.Context(), store.PutRequest{
					Repo:    repo,
					Path:    file.LocalPath,
					Content: file.Content,
				})
				if err != nil {
					return err
				}
				entries = append(entries, syncmanifest.Entry{
					Local:        file.LocalPath,
					RemoteDoc:    resolveRemoteDoc(resolver, repo, file.LocalPath, resp.Node.Path),
					Mount:        cleanSyncMount(root),
					ContentHash:  file.ContentHash,
					Size:         file.Size,
					MTime:        file.MTime.Format(time.RFC3339),
					Materialized: true,
				})
			}

			if manifestPath == "" {
				manifestPath = filepath.Join(".gxfs", "manifest.toml")
			}
			manifest := syncmanifest.Manifest{}
			if existing, err := syncmanifest.Load(manifestPath); err == nil {
				manifest = existing
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			manifest = syncmanifest.Upsert(manifest, entries)
			if err := syncmanifest.Save(manifestPath, manifest); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "pushed %d file%s from %s\n", len(files), plural(len(files)), root)
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	return cmd
}

func cleanSyncMount(root string) string {
	root = filepath.ToSlash(filepath.Clean(root))
	root = strings.Trim(root, "/")
	if root == "." {
		return ""
	}
	return root
}

// resolveRemoteDoc returns the correct remote_doc reference for a file.
// When a mount resolver is available, it resolves the local path to get the
// true remote repo and path. Otherwise it falls back to the response node path.
func resolveRemoteDoc(resolver *mountadapter.Resolver, repo, localPath, fallbackPath string) string {
	if resolver != nil {
		if resolved, err := resolver.Resolve(localPath, mountadapter.OpWrite); err == nil {
			return formatSourceRemoteRef(repo, resolved.Source)
		}
	}
	return "repo://self/" + strings.Trim(fallbackPath, "/")
}

// formatRemoteRef returns a repo:// ref string. Same-repo uses "self",
// cross-repo uses the URL-encoded repo name.
func formatRemoteRef(currentRepo, remoteRepo, remotePath string) string {
	return formatSourceRemoteRef(currentRepo, store.SourceRef{
		Kind: store.SourceKindRepo,
		Name: remoteRepo,
		Path: remotePath,
	})
}

func formatSourceRemoteRef(currentRepo string, source store.SourceRef) string {
	if source.Kind == store.SourceKindRepo && source.Name == currentRepo {
		source.Name = "self"
	}
	if source.Path != "" && !strings.HasPrefix(source.Path, "/") {
		source.Path = "/" + source.Path
	}
	return source.String()
}

func newSyncPullCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var manifestPath string
	var materialize bool
	var forceLocal bool
	var forceRemote bool
	cmd := &cobra.Command{
		Use:   "pull <local-path>",
		Short: "Pull GXFS docs into the local sync manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if forceLocal && forceRemote {
				return fmt.Errorf("--force-local and --force-remote are mutually exclusive")
			}
			root := args[0]
			if manifestPath == "" {
				manifestPath = filepath.Join(".gxfs", "manifest.toml")
			}

			manifest, err := loadManifest(manifestPath)
			if err != nil {
				return err
			}

			plan, err := buildRemoteSyncPlanForRoot(cmd.Context(), adapter, rawAdapter, resolver, repo, root, manifest, remoteSyncOptions{
				Materialize: materialize,
				ForceLocal:  forceLocal,
				ForceRemote: forceRemote,
			})
			if err != nil {
				return err
			}
			result, err := applyRemoteSyncPlan(cmd.Context(), adapter, rawAdapter, repo, root, manifestPath, manifest, plan, resolver)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "pulled %d file%s from %s\n", result.RemoteFiles, plural(result.RemoteFiles), root)
			if materialize {
				fmt.Fprintf(cmd.OutOrStdout(), "materialized %d file%s under %s\n", result.Materialized, plural(result.Materialized), root)
			}
			if result.ForcePushed > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "pushed %d local file%s to resolve conflicts\n", result.ForcePushed, plural(result.ForcePushed))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	cmd.Flags().BoolVar(&materialize, "materialize", false, "write pulled docs to local files")
	cmd.Flags().BoolVar(&forceLocal, "force-local", false, "resolve conflicts by pushing local content")
	cmd.Flags().BoolVar(&forceRemote, "force-remote", false, "resolve conflicts by accepting remote content")
	return cmd
}

type remoteSyncFile struct {
	LocalPath    string
	RemoteRepo   string
	RemotePath   string
	RemoteSource store.SourceRef
	Content      string
	ContentHash  string
	Size         int64
	MTime        string
}

type localSyncFile struct {
	LocalPath   string
	Content     string
	ContentHash string
	Size        int64
	MTime       time.Time
}

type remoteSyncOptions struct {
	Materialize bool
	ForceLocal  bool
	ForceRemote bool
}

type remoteSyncAction int

const (
	remoteSyncAccept remoteSyncAction = iota
	remoteSyncMaterialize
	remoteSyncPushLocal
)

type remoteSyncPlan struct {
	Changes []remoteSyncChange
}

type remoteSyncChange struct {
	Action remoteSyncAction
	Remote remoteSyncFile
	Local  *localSyncFile
	Entry  syncmanifest.Entry
}

type remoteSyncResult struct {
	Manifest     syncmanifest.Manifest
	RemoteFiles  int
	Materialized int
	ForcePushed  int
}

// collectRemoteFilesMetadata fetches file metadata + known hashes without content.
// Returns file nodes and a map of path->hash for files with known hashes.
func collectRemoteFilesMetadata(ctx context.Context, adapter store.Adapter, repo, root string) ([]store.Node, map[string]string, error) {
	stat, err := adapter.Stat(ctx, store.StatRequest{Repo: repo, Path: root})
	if err != nil {
		return nil, nil, err
	}

	nodes := []store.Node{stat.Node}
	if stat.Node.Kind == "dir" {
		resp, err := adapter.LS(ctx, store.LSRequest{Repo: repo, Path: root, Recursive: true, All: true})
		if err != nil {
			return nil, nil, err
		}
		nodes = resp.Nodes
	}

	var fileNodes []store.Node
	for _, node := range nodes {
		if node.Kind == "file" {
			fileNodes = append(fileNodes, node)
		}
	}

	// Fetch known hashes in one query
	hashResp, err := adapter.BatchHashes(ctx, store.HashRequest{Repo: repo, Path: root})
	if err != nil {
		return nil, nil, fmt.Errorf("batch hashes: %w", err)
	}
	hashMap := make(map[string]string, len(hashResp.Hashes))
	for _, ch := range hashResp.Hashes {
		hashMap[ch.Path] = ch.Hash
	}

	return fileNodes, hashMap, nil
}

// fetchChangedFileContents Cats only the files that need content (hash unknown or changed).
// For unchanged files, returns a remoteSyncFile with Content="" and the known hash.
func fetchChangedFileContents(ctx context.Context, adapter store.Adapter, repo string, fileNodes []store.Node, hashMap map[string]string, manifest syncmanifest.Manifest, localPathFn func(store.Node) string, sourceFn func(store.Node) store.SourceRef) ([]remoteSyncFile, error) {
	existingByLocal := manifestEntriesByLocal(manifest)

	// Determine which files need Cat
	var needCat []int // indices into fileNodes
	files := make([]remoteSyncFile, len(fileNodes))
	for i, node := range fileNodes {
		localPath := localPathFn(node)
		hash, hashKnown := hashMap[node.Path]
		existing, hasExisting := existingByLocal[localPath]

		if hashKnown && hasExisting && existing.ContentHash == hash {
			source := sourceFn(node)
			// Unchanged — no Cat needed
			files[i] = remoteSyncFile{
				LocalPath:    localPath,
				RemoteRepo:   repo,
				RemotePath:   node.Path,
				RemoteSource: source,
				ContentHash:  hash,
				Size:         node.Size,
				MTime:        node.ModTime,
			}
			continue
		}
		needCat = append(needCat, i)
	}

	// Cat only changed/unknown files
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, idx := range needCat {
		i := idx
		node := fileNodes[i]
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			cat, err := adapter.Cat(gctx, store.CatRequest{Repo: repo, Path: node.Path})
			if err != nil {
				return err
			}
			source := sourceFn(node)
			files[i] = remoteSyncFile{
				LocalPath:    localPathFn(node),
				RemoteRepo:   repo,
				RemotePath:   node.Path,
				RemoteSource: source,
				Content:      cat.Content,
				ContentHash:  store.HashContent(cat.Content),
				Size:         int64(len(cat.Content)),
				MTime:        node.ModTime,
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return files, nil
}

func collectRemoteFiles(ctx context.Context, adapter store.Adapter, repo, root string, manifest syncmanifest.Manifest) ([]remoteSyncFile, error) {
	fileNodes, hashMap, err := collectRemoteFilesMetadata(ctx, adapter, repo, root)
	if err != nil {
		return nil, err
	}
	return fetchChangedFileContents(ctx, adapter, repo, fileNodes, hashMap, manifest, func(node store.Node) string {
		return strings.Trim(strings.TrimPrefix(filepath.ToSlash(node.Path), "./"), "/")
	}, func(node store.Node) store.SourceRef {
		return store.SourceRef{Kind: store.SourceKindRepo, Name: repo, Path: node.Path}
	})
}

// collectMountedRemoteFiles collects remote files for a mounted path by resolving
// the local root through the mount resolver, fetching from the raw adapter using
// the real remote path, then mapping node paths back to local paths.
// This ensures RemotePath contains the true server path for correct remote_doc
// in the manifest, rather than the localized display path.
func collectMountedRemoteFiles(ctx context.Context, rawAdapter store.Adapter, resolver *mountadapter.Resolver, repo, localRoot string, manifest syncmanifest.Manifest) ([]remoteSyncFile, error) {
	resolved, err := resolver.Resolve(localRoot, mountadapter.OpRead)
	if err != nil {
		return nil, fmt.Errorf("resolve mount %s: %w", localRoot, err)
	}
	sourceAdapter, err := adapterForCommandSource(ctx, rawAdapter, resolved.Source)
	if err != nil {
		return nil, fmt.Errorf("resolve mount source %s: %w", resolved.Source.String(), err)
	}
	remoteSource := resolved.Source
	stat, err := sourceAdapter.Stat(ctx, store.StatRequest{Repo: remoteSource.Name, Path: remoteSource.Path})
	if err != nil {
		return nil, err
	}
	nodes := []store.Node{stat.Node}
	if stat.Node.Kind == "dir" {
		resp, err := sourceAdapter.LS(ctx, store.LSRequest{Repo: remoteSource.Name, Path: remoteSource.Path, Recursive: true, All: true})
		if err != nil {
			return nil, err
		}
		nodes = resp.Nodes
	}

	var fileNodes []store.Node
	for _, node := range nodes {
		if node.Kind == "file" {
			fileNodes = append(fileNodes, node)
		}
	}

	remoteRoot := strings.TrimSuffix(remoteSource.Path, "/")
	localBase := resolved.LocalPath

	// Fetch known hashes for hash-skip optimization
	hashResp, err := sourceAdapter.BatchHashes(ctx, store.HashRequest{Repo: remoteSource.Name, Path: remoteSource.Path})
	if err != nil {
		return nil, fmt.Errorf("batch hashes: %w", err)
	}
	hashMap := make(map[string]string, len(hashResp.Hashes))
	for _, ch := range hashResp.Hashes {
		hashMap[ch.Path] = ch.Hash
	}

	localPathFn := func(node store.Node) string {
		rel := strings.TrimPrefix(node.Path, remoteRoot+"/")
		rel = strings.TrimPrefix(rel, remoteRoot)
		localPath := localBase
		if rel != "" {
			localPath = localBase + "/" + rel
		}
		return localPath
	}
	return fetchChangedFileContents(ctx, sourceAdapter, remoteSource.Name, fileNodes, hashMap, manifest, localPathFn, func(node store.Node) store.SourceRef {
		source := remoteSource
		source.Path = node.Path
		return source
	})
}

func buildRemoteSyncPlan(ctx context.Context, adapter store.Adapter, repo, root string, manifest syncmanifest.Manifest, opts remoteSyncOptions) (remoteSyncPlan, error) {
	remoteFiles, err := collectRemoteFiles(ctx, adapter, repo, root, manifest)
	if err != nil {
		return remoteSyncPlan{}, err
	}
	return buildRemoteSyncPlanFromFiles(repo, remoteFiles, manifest, opts, root)
}

// buildRemoteSyncPlanForRoot picks the correct source resolution strategy based on
// whether a mount resolver is available, then builds the sync plan.
// When resolver != nil, it uses the raw adapter + resolver so that RemotePath
// contains the true server path (not the localized display path).
func buildRemoteSyncPlanForRoot(ctx context.Context, adapter, rawAdapter store.Adapter, resolver *mountadapter.Resolver, repo, root string, manifest syncmanifest.Manifest, opts remoteSyncOptions) (remoteSyncPlan, error) {
	if resolver != nil {
		remoteFiles, err := collectMountedRemoteFiles(ctx, rawAdapter, resolver, repo, root, manifest)
		if err != nil {
			return remoteSyncPlan{}, err
		}
		return buildRemoteSyncPlanFromFiles(repo, remoteFiles, manifest, opts, root)
	}
	return buildRemoteSyncPlan(ctx, adapter, repo, root, manifest, opts)
}

func buildRemoteSyncPlanFromFiles(repo string, remoteFiles []remoteSyncFile, manifest syncmanifest.Manifest, opts remoteSyncOptions, root string) (remoteSyncPlan, error) {
	existingByLocal := manifestEntriesByLocal(manifest)
	changes := make([]remoteSyncChange, 0, len(remoteFiles))
	for _, remote := range remoteFiles {
		existing, hasExisting := existingByLocal[remote.LocalPath]
		local, localExists, err := readLocalSyncFile(remote.LocalPath)
		if err != nil {
			return remoteSyncPlan{}, err
		}

		remoteChanged := !hasExisting || existing.ContentHash != remote.ContentHash
		localChanged := localExists && hasExisting && local.ContentHash != existing.ContentHash
		untrackedLocalConflict := opts.Materialize && localExists && !hasExisting && local.ContentHash != remote.ContentHash

		if localChanged && remoteChanged {
			if opts.ForceLocal {
				changes = append(changes, remoteSyncChange{Action: remoteSyncPushLocal, Remote: remote, Local: &local})
				continue
			}
			if !opts.ForceRemote {
				return remoteSyncPlan{}, fmt.Errorf("conflict on %s: local and remote both changed (use --force-local or --force-remote)", remote.LocalPath)
			}
		}
		if opts.Materialize && (localChanged || untrackedLocalConflict) && !opts.ForceRemote {
			if opts.ForceLocal && localExists {
				changes = append(changes, remoteSyncChange{Action: remoteSyncPushLocal, Remote: remote, Local: &local})
				continue
			}
			return remoteSyncPlan{}, fmt.Errorf("local file %s has unpushed changes (use --force-local or --force-remote)", remote.LocalPath)
		}

		action := remoteSyncAccept
		entry := remote.toManifestEntry(repo, root, false)
		if opts.Materialize {
			action = remoteSyncMaterialize
			entry.Materialized = true
		} else if localExists && local.ContentHash == remote.ContentHash {
			entry.Materialized = true
		}
		changes = append(changes, remoteSyncChange{Action: action, Remote: remote, Entry: entry})
	}
	return remoteSyncPlan{Changes: changes}, nil
}

func applyRemoteSyncPlan(ctx context.Context, adapter store.Adapter, rawAdapter store.Adapter, repo, root, manifestPath string, manifest syncmanifest.Manifest, plan remoteSyncPlan, resolver *mountadapter.Resolver) (remoteSyncResult, error) {
	result := remoteSyncResult{RemoteFiles: len(plan.Changes)}
	entries := make([]syncmanifest.Entry, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		switch change.Action {
		case remoteSyncAccept:
			entries = append(entries, change.Entry)
		case remoteSyncPushLocal:
			entry, err := pushLocalConflict(ctx, adapter, repo, *change.Local, change.Remote, root, resolver)
			if err != nil {
				return remoteSyncResult{}, err
			}
			entries = append(entries, entry)
			result.ForcePushed++
		case remoteSyncMaterialize:
			content := change.Remote.Content
			if content == "" {
				// Hash-skipped file: fetch content on demand for materialization.
				// Use the source adapter when available so docs:// mounts keep
				// their namespace identity.
				catAdapter := adapter
				if rawAdapter != nil {
					catAdapter = rawAdapter
				}
				catRepo := change.Remote.RemoteRepo
				catPath := change.Remote.RemotePath
				if change.Remote.RemoteSource.Kind != "" {
					var err error
					catAdapter, err = adapterForCommandSource(ctx, catAdapter, change.Remote.RemoteSource)
					if err != nil {
						return remoteSyncResult{}, fmt.Errorf("resolve source %s for materialize: %w", change.Remote.RemoteSource.String(), err)
					}
					catRepo = change.Remote.RemoteSource.Name
					catPath = change.Remote.RemoteSource.Path
				}
				if catRepo == "" {
					catRepo = repo
				}
				cat, err := catAdapter.Cat(ctx, store.CatRequest{Repo: catRepo, Path: catPath})
				if err != nil {
					return remoteSyncResult{}, fmt.Errorf("cat %s for materialize: %w", catPath, err)
				}
				content = cat.Content
			}
			if err := writeMaterializedFile(change.Remote.LocalPath, content); err != nil {
				return remoteSyncResult{}, err
			}
			entries = append(entries, change.Entry)
			result.Materialized++
		default:
			return remoteSyncResult{}, fmt.Errorf("unknown sync action: %d", change.Action)
		}
	}

	manifest = syncmanifest.ReplaceUnder(manifest, root, entries)
	if err := syncmanifest.Save(manifestPath, manifest); err != nil {
		return remoteSyncResult{}, err
	}
	result.Manifest = manifest
	return result, nil
}

// remoteDoc returns the source ref for this file for manifest entries.
func (f remoteSyncFile) remoteDoc(currentRepo string) string {
	if f.RemoteSource.Kind != "" {
		return formatSourceRemoteRef(currentRepo, f.RemoteSource)
	}
	return formatRemoteRef(currentRepo, f.RemoteRepo, f.RemotePath)
}

func (f remoteSyncFile) toManifestEntry(currentRepo, root string, materialized bool) syncmanifest.Entry {
	return syncmanifest.Entry{
		Local:        f.LocalPath,
		RemoteDoc:    f.remoteDoc(currentRepo),
		Mount:        cleanSyncMount(root),
		ContentHash:  f.ContentHash,
		Size:         f.Size,
		MTime:        f.MTime,
		Materialized: materialized,
	}
}

func manifestEntriesByLocal(manifest syncmanifest.Manifest) map[string]syncmanifest.Entry {
	entries := make(map[string]syncmanifest.Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entries[entry.Local] = entry
	}
	return entries
}

func readLocalSyncFile(localPath string) (localSyncFile, bool, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return localSyncFile{}, false, nil
		}
		return localSyncFile{}, false, fmt.Errorf("stat %s: %w", localPath, err)
	}
	if info.IsDir() {
		return localSyncFile{}, false, fmt.Errorf("local path %s is a directory", localPath)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return localSyncFile{}, false, fmt.Errorf("read %s: %w", localPath, err)
	}
	content := string(data)
	return localSyncFile{
		LocalPath:   localPath,
		Content:     content,
		ContentHash: store.HashContent(content),
		Size:        info.Size(),
		MTime:       info.ModTime().UTC(),
	}, true, nil
}

func pushLocalConflict(ctx context.Context, adapter store.Adapter, repo string, local localSyncFile, remote remoteSyncFile, root string, resolver *mountadapter.Resolver) (syncmanifest.Entry, error) {
	resp, err := adapter.Put(ctx, store.PutRequest{
		Repo:    repo,
		Path:    remote.LocalPath,
		Content: local.Content,
	})
	if err != nil {
		return syncmanifest.Entry{}, err
	}
	return syncmanifest.Entry{
		Local:        local.LocalPath,
		RemoteDoc:    resolveRemoteDoc(resolver, repo, remote.LocalPath, resp.Node.Path),
		Mount:        cleanSyncMount(root),
		ContentHash:  local.ContentHash,
		Size:         local.Size,
		MTime:        local.MTime.Format(time.RFC3339),
		Materialized: true,
	}, nil
}

func writeMaterializedFile(localPath, content string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(localPath), err)
	}
	if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", localPath, err)
	}
	return nil
}

func defaultManifestPath(path string) string {
	if path != "" {
		return path
	}
	return filepath.Join(".gxfs", "manifest.toml")
}

func loadManifest(path string) (syncmanifest.Manifest, error) {
	manifest := syncmanifest.Manifest{}
	if existing, err := syncmanifest.Load(path); err == nil {
		return existing, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return syncmanifest.Manifest{}, err
	}
	return manifest, nil
}

func refreshManifest(ctx context.Context, adapter, rawAdapter store.Adapter, repo, root, manifestPath string, materialize bool, resolver *mountadapter.Resolver) (remoteSyncResult, error) {
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return remoteSyncResult{}, err
	}

	plan, err := buildRemoteSyncPlanForRoot(ctx, adapter, rawAdapter, resolver, repo, root, manifest, remoteSyncOptions{Materialize: materialize})
	if err != nil {
		return remoteSyncResult{}, err
	}
	result, err := applyRemoteSyncPlan(ctx, adapter, rawAdapter, repo, root, manifestPath, manifest, plan, resolver)
	if err != nil {
		return remoteSyncResult{}, err
	}
	return result, nil
}

func NewRefreshCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var manifestPath string
	cmd := &cobra.Command{
		Use:   "refresh <path>",
		Short: "Refresh the local GXFS manifest for a path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			manifestPath = defaultManifestPath(manifestPath)
			result, err := refreshManifest(cmd.Context(), adapter, rawAdapter, repo, root, manifestPath, false, resolver)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "refreshed %d file%s under %s\n", result.RemoteFiles, plural(result.RemoteFiles), root)
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	return cmd
}

func NewMaterializeCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	var manifestPath string
	cmd := &cobra.Command{
		Use:   "materialize <path>",
		Short: "Write GXFS docs under a path to local markdown files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			manifestPath = defaultManifestPath(manifestPath)
			result, err := refreshManifest(cmd.Context(), adapter, rawAdapter, repo, root, manifestPath, true, resolver)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "refreshed %d file%s under %s\n", result.RemoteFiles, plural(result.RemoteFiles), root)
			fmt.Fprintf(cmd.OutOrStdout(), "materialized %d file%s under %s\n", result.Materialized, plural(result.Materialized), root)
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	return cmd
}

func NewDematerializeCommand() *cobra.Command {
	var manifestPath string
	var keepFiles bool
	cmd := &cobra.Command{
		Use:   "dematerialize <path>",
		Short: "Mark materialized GXFS docs as remote-only",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := args[0]
			manifestPath = defaultManifestPath(manifestPath)
			manifest, err := syncmanifest.Load(manifestPath)
			if err != nil {
				return err
			}
			entries := syncmanifest.EntriesUnder(manifest, root)
			if len(entries) == 0 {
				return fmt.Errorf("no manifest entries under %s", root)
			}

			dematerialized := 0
			for i := range entries {
				if entries[i].Materialized && !keepFiles {
					if err := removeMaterializedFile(entries[i].Local, root); err != nil {
						return err
					}
				}
				if entries[i].Materialized {
					dematerialized++
				}
				entries[i].Materialized = false
			}
			manifest = syncmanifest.UpdateEntries(manifest, entries)
			if err := syncmanifest.Save(manifestPath, manifest); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "dematerialized %d file%s under %s\n", dematerialized, plural(dematerialized), root)
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "manifest path (default .gxfs/manifest.toml)")
	cmd.Flags().BoolVar(&keepFiles, "keep-files", false, "update manifest without deleting local files")
	return cmd
}

func removeMaterializedFile(localPath, root string) error {
	if err := os.Remove(localPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", localPath, err)
	}
	if err := removeEmptyParents(localPath, root); err != nil {
		return err
	}
	return nil
}

func removeEmptyParents(localPath, root string) error {
	dir := filepath.Clean(filepath.Dir(localPath))
	stop := filepath.Clean(root)
	if stop == "." {
		return nil
	}

	for dir != "." && dir != string(filepath.Separator) {
		if dir == stop {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("read dir %s: %w", dir, err)
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("remove dir %s: %w", dir, err)
		}
		dir = filepath.Dir(dir)
	}
	return nil
}

// --- Hook commands ---
