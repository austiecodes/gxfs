package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/austiecodes/gxfs/cmd/gxfs/command"
	"github.com/austiecodes/gxfs/internal/client"
	"github.com/austiecodes/gxfs/internal/config"
	mountadapter "github.com/austiecodes/gxfs/internal/mount"
	"github.com/austiecodes/gxfs/internal/store"
)

func newRootCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gxfs",
		Short: "Inspect GXFS virtual filesystems",
		Long:  "GXFS gives agents Unix-like commands for virtual filesystem content served by gxfs-server.",
	}

	// Try to get collection client from rawAdapter
	var collectionClient *client.Client
	if cli, ok := rawAdapter.(*client.Client); ok {
		collectionClient = cli
	}

	cmd.AddCommand(command.NewLSCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(command.NewTreeCommand(adapter, repo))
	cmd.AddCommand(command.NewCatCommand(adapter, rawAdapter, repo, collectionClient))
	cmd.AddCommand(command.NewGrepCommand(adapter, repo))
	cmd.AddCommand(command.NewFindCommand(adapter, repo))
	cmd.AddCommand(command.NewStatCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(command.NewWriteCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewEditCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewDeleteCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewSearchCommand(adapter, repo))
	cmd.AddCommand(command.NewLocateCommand(rawAdapter, repo))
	cmd.AddCommand(command.NewRefreshCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewMaterializeCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewDematerializeCommand())
	cmd.AddCommand(command.NewInitCommand())
	cmd.AddCommand(command.NewConfigCommand(repo))
	cmd.AddCommand(command.NewSyncCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewMountCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(command.NewRepoCommand(rawAdapter))
	cmd.AddCommand(command.NewGlobCommand(rawAdapter, repo))
	cmd.AddCommand(command.NewAttachCommand(rawAdapter, repo))
	cmd.AddCommand(command.NewHookCommand(adapter, rawAdapter, repo, resolver))
	if collectionClient != nil {
		cmd.AddCommand(command.NewCollectionCommand(collectionClient))
	}
	return cmd
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	logID := os.Getenv("GXFS_LOG_ID")
	start := time.Now()
	commandName := ""
	if len(args) > 0 {
		commandName = args[0]
	}

	exitCode := runInner(args, stdout, stderr)

	appendAudit(logID, commandName, time.Since(start).Milliseconds(), exitCode)
	return exitCode
}

func runInner(args []string, stdout, stderr io.Writer) int {
	if isConfigFreeCommand(args) {
		cmd := newRootCommand(nil, nil, "", nil)
		cmd.SetArgs(args)
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		if err := cmd.Execute(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	path := os.Getenv("GXFS_CONFIG")
	if path == "" {
		path = ".gxfs/settings.toml"
	}
	cfg, err := config.LoadCLI(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	rawClient := client.New(cfg.Server.Addr)
	rawClient.SetClientRepo(cfg.Repo)
	rawClient.SetLogID(os.Getenv("GXFS_LOG_ID"))

	adapter, resolver, err := loadRuntimeAdapter(cfg, path, rawClient)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	cmd := newRootCommand(adapter, rawClient, cfg.Repo, resolver)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func isConfigFreeCommand(args []string) bool {
	if wantsHelp(args) {
		return true
	}
	return len(args) > 0 && args[0] == "init"
}

func loadRuntimeAdapter(cfg config.CLIConfig, settingsPath string, rawClient store.Adapter) (store.Adapter, *mountadapter.Resolver, error) {
	mountsPath := filepath.Join(filepath.Dir(settingsPath), "mounts.toml")
	mountsCfg, err := config.LoadMounts(mountsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			mountsCfg = config.DefaultMounts(cfg)
		} else {
			return nil, nil, err
		}
	}

	resolver, err := mountadapter.NewResolver(cfg.Repo, mountsCfg.Mounts)
	if err != nil {
		return nil, nil, err
	}

	return mountadapter.NewAdapter(rawClient, resolver), resolver, nil
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "help" || arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}
