package cli

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(pruneCmd)
}

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove cached downloads and manifest",
	Long:  "Remove cached archive downloads and the manifest cache to free disk space.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		manager := getManager(cmd)
		removed, bytes, err := manager.Prune()
		if err != nil {
			return err
		}
		if removed == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Nothing to prune.")
			return nil
		}
		green := color.New(color.Bold, color.FgGreen).SprintFunc()
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s Removed %d cached file(s) (%.1f MB)\n",
			green("✓"), removed, float64(bytes)/(1<<20))
		return nil
	},
}
