package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"gxfs/internal/client"
	"gxfs/internal/store"
)

func newCollectionCommand(cli *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collection",
		Short: "Collection management commands",
	}

	cmd.AddCommand(newCollectionCreateCommand(cli))
	cmd.AddCommand(newCollectionListCommand(cli))
	cmd.AddCommand(newCollectionShowCommand(cli))
	cmd.AddCommand(newCollectionAddCommand(cli))
	cmd.AddCommand(newCollectionRmCommand(cli))
	return cmd
}

func writeJSONOutput(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func newCollectionCreateCommand(cli *client.Client) *cobra.Command {
	var description string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			resp, err := cli.CreateCollection(cmd.Context(), store.CreateCollectionRequest{
				Name:        name,
				Description: description,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created collection: %s (id: %s)\n", resp.Collection.Name, resp.Collection.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "Collection description")
	return cmd
}

func newCollectionListCommand(cli *client.Client) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all collections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := cli.ListCollections(cmd.Context())
			if err != nil {
				return err
			}

			if jsonOutput {
				return writeJSONOutput(cmd, resp)
			}

			if len(resp.Collections) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No collections found.")
				return nil
			}

			for _, col := range resp.Collections {
				fmt.Fprintf(cmd.OutOrStdout(), "%s", col.Name)
				if col.Description != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "\t%s", col.Description)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newCollectionShowCommand(cli *client.Client) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show collection details and members",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			resp, err := cli.GetCollection(cmd.Context(), name)
			if err != nil {
				return err
			}

			if jsonOutput {
				return writeJSONOutput(cmd, resp)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Collection: %s\n", resp.Collection.Name)
			if resp.Collection.Description != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Description: %s\n", resp.Collection.Description)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n", resp.Collection.CreatedAt)
			fmt.Fprintf(cmd.OutOrStdout(), "Updated: %s\n", resp.Collection.UpdatedAt)
			fmt.Fprintln(cmd.OutOrStdout())

			if len(resp.Members) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No members.")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Members (%d):\n", len(resp.Members))
			for _, m := range resp.Members {
				fmt.Fprintf(cmd.OutOrStdout(), "  collection://%s%s (doc_id: %s)\n", resp.Collection.Name, m.Path, m.DocID)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newCollectionAddCommand(cli *client.Client) *cobra.Command {
	var source string

	cmd := &cobra.Command{
		Use:   "add <name> <collection_path>",
		Short: "Add a document to a collection",
		Long: `Add a document from a repo to a collection.

The source must be a repo:// URL, e.g. repo://my-repo/docs/readme.md

Example:
  gxfs collection add best-practices /go-errors.md --source repo://openai-go/errors.md`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := args[1]

			if source == "" {
				return fmt.Errorf("--source is required")
			}

			resp, err := cli.AddMember(cmd.Context(), store.AddMemberRequest{
				Name:      name,
				SourceRef: source,
				Path:      path,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added to collection: %s -> %s (doc_id: %s)\n", path, name, resp.Member.DocID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&source, "source", "s", "", "Source document (repo://repo-name/path)")
	cmd.MarkFlagRequired("source")
	return cmd
}

func newCollectionRmCommand(cli *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <name> <collection_path>",
		Short: "Remove a document from a collection",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := args[1]

			if err := cli.RemoveMember(cmd.Context(), name, path); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Removed from collection: %s -> %s\n", name, path)
			return nil
		},
	}

	return cmd
}
