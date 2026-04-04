package cli

import (
	"fmt"

	"github.com/fatih/color"
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
		manager := getManager(cmd)
		version, err := manager.ResolveVersion(ctx, args[0])
		if err != nil {
			return err
		}
		if err := manager.Install(ctx, version); err != nil {
			return err
		}
		green := color.New(color.Bold, color.FgGreen).SprintFunc()
		blue := color.New(color.Bold, color.FgBlue).SprintFunc()
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s Installed go %s\n", green("✓"), version)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s To activate: fgm use %s\n", blue("→"), version)
		return nil
	},
}
