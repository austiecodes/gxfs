package command

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/austiecodes/rolio/internal/client"
	"github.com/austiecodes/rolio/internal/store"
)

func NewDocsetCommand(cli *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docset",
		Short: "Docset management commands",
	}

	cmd.AddCommand(newDocsetCreateCommand(cli))
	cmd.AddCommand(newDocsetListCommand(cli))
	cmd.AddCommand(newDocsetShowCommand(cli))
	cmd.AddCommand(newDocsetAddCommand(cli))
	cmd.AddCommand(newDocsetRmCommand(cli))
	return cmd
}

func requireDocsetClient(cli *client.Client) error {
	if cli != nil {
		return nil
	}
	return fmt.Errorf("docset commands require a configured rolio server connection")
}

func writeJSONOutput(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func newDocsetCreateCommand(cli *client.Client) *cobra.Command {
	var description string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new docset",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDocsetClient(cli); err != nil {
				return err
			}
			name := args[0]
			resp, err := cli.CreateDocset(cmd.Context(), store.CreateDocsetRequest{
				Name:        name,
				Description: description,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created docset: %s (id: %s)\n", resp.Docset.Name, resp.Docset.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "Docset description")
	return cmd
}

func newDocsetListCommand(cli *client.Client) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all docsets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDocsetClient(cli); err != nil {
				return err
			}
			resp, err := cli.ListDocsets(cmd.Context())
			if err != nil {
				return err
			}

			if jsonOutput {
				return writeJSONOutput(cmd, resp)
			}

			if len(resp.Docsets) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No docsets found.")
				return nil
			}

			for _, docset := range resp.Docsets {
				fmt.Fprintf(cmd.OutOrStdout(), "%s", docset.Name)
				if docset.Description != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "\t%s", docset.Description)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newDocsetShowCommand(cli *client.Client) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show docset details and members",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDocsetClient(cli); err != nil {
				return err
			}
			name := args[0]
			resp, err := cli.GetDocset(cmd.Context(), name)
			if err != nil {
				return err
			}

			if jsonOutput {
				return writeJSONOutput(cmd, resp)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Docset: %s\n", resp.Docset.Name)
			if resp.Docset.Description != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Description: %s\n", resp.Docset.Description)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n", resp.Docset.CreatedAt)
			fmt.Fprintf(cmd.OutOrStdout(), "Updated: %s\n", resp.Docset.UpdatedAt)
			fmt.Fprintln(cmd.OutOrStdout())

			if len(resp.Members) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No members.")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Members (%d):\n", len(resp.Members))
			for _, m := range resp.Members {
				fmt.Fprintf(cmd.OutOrStdout(), "  docset://%s%s (doc_id: %s)\n", resp.Docset.Name, m.Path, m.DocID)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newDocsetAddCommand(cli *client.Client) *cobra.Command {
	var source string

	cmd := &cobra.Command{
		Use:   "add <name> <docset_path>",
		Short: "Add a document to a docset",
		Long: `Add a document from a repo to a docset.

The source must be a repo:// URL, e.g. repo://my-repo/docs/readme.md

Example:
  rolio docset add best-practices /go-errors.md --source repo://openai-go/errors.md`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDocsetClient(cli); err != nil {
				return err
			}
			name := args[0]
			path := args[1]

			if source == "" {
				return fmt.Errorf("--source is required")
			}

			resp, err := cli.AddDocsetMember(cmd.Context(), store.AddDocsetMemberRequest{
				Name:      name,
				SourceRef: source,
				Path:      path,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added to docset: %s -> %s (doc_id: %s)\n", path, name, resp.Member.DocID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&source, "source", "s", "", "Source document (repo://repo-name/path)")
	cmd.MarkFlagRequired("source")
	return cmd
}

func newDocsetRmCommand(cli *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <name> <docset_path>",
		Short: "Remove a document from a docset",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireDocsetClient(cli); err != nil {
				return err
			}
			name := args[0]
			path := args[1]

			if err := cli.RemoveDocsetMember(cmd.Context(), name, path); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Removed from docset: %s -> %s\n", name, path)
			return nil
		},
	}

	return cmd
}
