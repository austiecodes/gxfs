package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/austiecodes/gxfs/internal/store"
)

func NewRepoCommand(rawAdapter store.Adapter) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Repository management commands",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newRepoListCommand(rawAdapter))
	return cmd
}

func newRepoListCommand(rawAdapter store.Adapter) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List available repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			lister, ok := rawAdapter.(repoLister)
			if !ok {
				return fmt.Errorf("repo listing is not supported by the current adapter")
			}
			repos, err := lister.RepoList(cmd.Context())
			if err != nil {
				return err
			}
			for _, name := range repos {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	}
	return cmd
}
