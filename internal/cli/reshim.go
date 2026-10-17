package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newReshimCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reshim",
		Short: "Regenerate the go and gofmt shims",
		Long: "Regenerate the go and gofmt shims.\n\n" +
			"Upgrading fgm replaces the fgm binary but leaves the existing shims in " +
			"place, so run this after an upgrade to pick up shim fixes. It is safe to " +
			"run at any time and does not change which version is active.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := getManager(cmd)
			if err != nil {
				return err
			}
			if err := manager.Reshim(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s Shims regenerated in %s\n", green("✓"), manager.ShimDir())
			return nil
		},
	}
}
