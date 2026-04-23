package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(installCmd)
}

var installCmd = &cobra.Command{
	Use:     "install <version>",
	Aliases: []string{"in"},
	Short:   "Install a Go version",
	Long:    "Install a Go version. Accepts exact (1.25.5), minor (1.25), or \"latest\".",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		version, err := manager.ResolveVersion(ctx, args[0])
		if err != nil {
			return err
		}
		alreadyInstalled := manager.IsInstalled(version)
		if err := manager.Install(ctx, version); err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if alreadyInstalled {
			fmt.Fprintf(out, "%s go %s already installed\n", blue("•"), version)
		} else {
			fmt.Fprintf(out, "%s Installed go %s\n", green("✓"), version)
		}
		fmt.Fprintf(out, "%s To activate: fgm use %s\n", blue("→"), version)
		return nil
	},
}
