package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"gxfs/internal/client"
	"gxfs/internal/config"
	"gxfs/internal/store"
)

func newRootCommand(adapter store.Adapter, repo string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gxfs",
		Short: "Inspect GXFS virtual filesystems",
		Long:  "GXFS gives agents Unix-like commands for virtual filesystem content served by gxfs-server.",
	}

	cmd.AddCommand(newLSCommand(adapter, repo))
	cmd.AddCommand(newTreeCommand(adapter, repo))
	cmd.AddCommand(newCatCommand(adapter, repo))
	cmd.AddCommand(newGrepCommand(adapter, repo))
	cmd.AddCommand(newFindCommand(adapter, repo))
	cmd.AddCommand(newStatCommand(adapter, repo))
	cmd.AddCommand(newConfigCommand(repo))
	return cmd
}

func newLSCommand(adapter store.Adapter, repo string) *cobra.Command {
	return &cobra.Command{
		Use:   "ls [path]",
		Short: "List VFS directory contents",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := argPath(args, "/")
			resp, err := adapter.LS(cmd.Context(), store.LSRequest{Repo: repo, Path: p})
			if err != nil {
				return err
			}
			for _, node := range resp.Nodes {
				name := node.Name
				if node.Kind == "dir" {
					name += "/"
				}
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	}
}

func newTreeCommand(adapter store.Adapter, repo string) *cobra.Command {
	var depth int
	cmd := &cobra.Command{
		Use:   "tree [path]",
		Short: "Print a VFS tree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adapter.Tree(cmd.Context(), store.TreeRequest{
				Repo: repo, Path: argPath(args, "/"), Depth: depth,
			})
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), resp.Text)
			return nil
		},
	}
	cmd.Flags().IntVarP(&depth, "level", "L", 2, "maximum tree depth")
	return cmd
}

func newCatCommand(adapter store.Adapter, repo string) *cobra.Command {
	return &cobra.Command{
		Use:   "cat <path>",
		Short: "Print VFS file content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adapter.Cat(cmd.Context(), store.CatRequest{Repo: repo, Path: args[0]})
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), resp.Content)
			return nil
		},
	}
}

func newGrepCommand(adapter store.Adapter, repo string) *cobra.Command {
	var regex bool
	cmd := &cobra.Command{
		Use:   "grep <pattern> [path]",
		Short: "Search VFS files",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adapter.Grep(cmd.Context(), store.GrepRequest{
				Repo: repo, Pattern: args[0], Path: argPath(args[1:], "/"), Regex: regex,
			})
			if err != nil {
				return err
			}
			for _, match := range resp.Matches {
				fmt.Fprintf(cmd.OutOrStdout(), "%s:%d:%s\n", match.Path, match.Line, match.Text)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&regex, "regex", "E", false, "treat pattern as a regular expression")
	return cmd
}

func newFindCommand(adapter store.Adapter, repo string) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "find [path] -name <glob>",
		Short: "Find VFS files by name",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("-name is required")
			}
			resp, err := adapter.Find(cmd.Context(), store.FindRequest{
				Repo: repo, Path: argPath(args, "/"), Name: name,
			})
			if err != nil {
				return err
			}
			for _, node := range resp.Nodes {
				fmt.Fprintln(cmd.OutOrStdout(), node.Path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "filename glob")
	return cmd
}

func newStatCommand(adapter store.Adapter, repo string) *cobra.Command {
	return &cobra.Command{
		Use:   "stat <path>",
		Short: "Print VFS node metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := adapter.Stat(cmd.Context(), store.StatRequest{Repo: repo, Path: args[0]})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Path: %s\n", resp.Node.Path)
			fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\n", resp.Node.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "Kind: %s\n", resp.Node.Kind)
			return nil
		},
	}
}

func newConfigCommand(repo string) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect GXFS CLI configuration",
	}
	configCmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check resolved GXFS CLI configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "Project: %s\n", repo)
			return nil
		},
	})
	return configCmd
}

func argPath(args []string, fallback string) string {
	if len(args) == 0 || args[0] == "" {
		return fallback
	}
	return args[0]
}

func main() {
	path := os.Getenv("GXFS_CONFIG")
	if path == "" {
		path = "gxfs.toml"
	}
	cfg, err := config.LoadCLI(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cmd := newRootCommand(client.New(cfg.Server.Addr), cfg.Project)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
