package cli

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(useCmd)
}

var useCmd = &cobra.Command{
	Use:   "use <version>",
	Short: "Install if needed, then activate a Go version",
	Long:  "Install if needed, then activate a Go version. Accepts exact (1.25.5), minor (1.25), or \"latest\".",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		manager := getManager(cmd)
		version, err := manager.ResolveVersion(ctx, args[0])
		if err != nil {
			return err
		}
		if err := manager.Use(ctx, version); err != nil {
			return err
		}
		green := color.New(color.Bold, color.FgGreen).SprintFunc()
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s Using go %s\n", green("✓"), version)
		return nil
	},
}
