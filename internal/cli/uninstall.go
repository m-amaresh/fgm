package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(uninstallCmd)
}

var uninstallCmd = &cobra.Command{
	Use:     "uninstall <version>",
	Aliases: []string{"un"},
	Short:   "Remove an installed Go version",
	Args:    cobra.ExactArgs(1),
	Long:    "Remove an installed Go version. Accepts exact (1.25.5), minor (1.25), or \"latest\" from locally installed versions.",
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		version, err := manager.ResolveInstalledVersion(args[0])
		if err != nil {
			return err
		}
		if err := manager.Uninstall(version); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s Uninstalled go %s\n", green("✓"), version)
		return nil
	},
}
