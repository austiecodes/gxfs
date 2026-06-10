package command

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewConfigCommand(repo string) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect ROLIO CLI configuration",
	}
	configCmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check resolved ROLIO CLI configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "Repo: %s\n", repo)
			return nil
		},
	})
	return configCmd
}
