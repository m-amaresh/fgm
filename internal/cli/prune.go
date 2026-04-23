package cli

import (
	"fmt"

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
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		removed, bytes, err := manager.Prune()
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if removed == 0 {
			fmt.Fprintln(w, "Nothing to prune.")
			return nil
		}
		fmt.Fprintf(w, "%s Removed %d cached file(s) (%.1f MB)\n",
			green("✓"), removed, float64(bytes)/(1<<20))
		return nil
	},
}
