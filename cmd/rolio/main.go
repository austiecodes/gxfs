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

	"github.com/austiecodes/rolio/cmd/rolio/command"
	"github.com/austiecodes/rolio/internal/client"
	"github.com/austiecodes/rolio/internal/config"
	mountadapter "github.com/austiecodes/rolio/internal/mount"
	"github.com/austiecodes/rolio/internal/store"
)

func newRootCommand(adapter, rawAdapter store.Adapter, repo string, resolver *mountadapter.Resolver) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rolio",
		Short: "Inspect ROLIO virtual filesystems",
		Long:  "ROLIO gives agents Unix-like commands for virtual filesystem content served by rolio-server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	// Try to get a docset client from rawAdapter.
	var docsetClient *client.Client
	if cli, ok := rawAdapter.(*client.Client); ok {
		docsetClient = cli
	}

	cmd.AddCommand(command.NewLSCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(command.NewTreeCommand(adapter, repo))
	cmd.AddCommand(command.NewCatCommand(adapter, rawAdapter, repo, docsetClient))
	cmd.AddCommand(command.NewGrepCommand(adapter, repo))
	cmd.AddCommand(command.NewFindCommand(adapter, repo))
	cmd.AddCommand(command.NewStatCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(command.NewWriteCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewEditCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewDeleteCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewSearchCommand(adapter, repo))
	cmd.AddCommand(command.NewLocateCommand(rawAdapter, repo))
	cmd.AddCommand(command.NewInitCommand())
	cmd.AddCommand(command.NewConfigCommand(repo))
	cmd.AddCommand(command.NewSyncCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewMountCommand(adapter, rawAdapter, repo))
	cmd.AddCommand(command.NewRepoCommand(rawAdapter))
	cmd.AddCommand(command.NewGlobCommand(rawAdapter, repo))
	cmd.AddCommand(command.NewHookCommand(adapter, rawAdapter, repo, resolver))
	cmd.AddCommand(command.NewDocsetCommand(docsetClient))
	return cmd
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	logID := os.Getenv("ROLIO_LOG_ID")
	sessionID := os.Getenv("ROLIO_SESSION_ID")
	start := time.Now()
	commandName := ""
	if len(args) > 0 {
		commandName = args[0]
	}

	result := runInner(args, stdout, stderr)
	durationMs := time.Since(start).Milliseconds()

	appendAudit(logID, sessionID, commandName, durationMs, result.exitCode)
	maybeReportUsageEvent(usageReportRequest{
		ServerAddr: result.serverAddr,
		Repo:       result.repo,
		LogID:      logID,
		SessionID:  sessionID,
		Command:    commandName,
		Args:       args,
		DurationMs: durationMs,
		ExitCode:   result.exitCode,
	})
	return result.exitCode
}

type runResult struct {
	exitCode   int
	serverAddr string
	repo       string
}

func runInner(args []string, stdout, stderr io.Writer) runResult {
	if isConfigFreeCommand(args) {
		cmd := newRootCommand(nil, nil, "", nil)
		cmd.SetArgs(args)
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		if err := cmd.Execute(); err != nil {
			fmt.Fprintln(stderr, err)
			return runResult{exitCode: 1}
		}
		return runResult{exitCode: 0}
	}

	path := os.Getenv("ROLIO_CONFIG")
	if path == "" {
		path = ".rolio/settings.toml"
	}
	cfg, err := config.LoadCLI(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return runResult{exitCode: 1}
	}

	rawClient := client.New(cfg.Server.Addr)
	rawClient.SetClientRepo(cfg.Repo)
	rawClient.SetLogID(os.Getenv("ROLIO_LOG_ID"))

	adapter, resolver, err := loadRuntimeAdapter(cfg, path, rawClient)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return runResult{exitCode: 1, serverAddr: cfg.Server.Addr, repo: cfg.Repo}
	}

	cmd := newRootCommand(adapter, rawClient, cfg.Repo, resolver)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return runResult{exitCode: 1, serverAddr: cfg.Server.Addr, repo: cfg.Repo}
	}
	return runResult{exitCode: 0, serverAddr: cfg.Server.Addr, repo: cfg.Repo}
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
