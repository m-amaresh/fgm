package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List installed Go versions",
	RunE: func(cmd *cobra.Command, _ []string) error {
		manager, err := getManager(cmd)
		if err != nil {
			return err
		}
		versions, current, err := manager.List()
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if len(versions) == 0 {
			fmt.Fprintln(w, "No Go versions installed. Run: fgm install latest")
			return nil
		}
		for _, version := range versions {
			if version == current {
				fmt.Fprintf(w, "%s (current)\n", green("* "+version))
			} else {
				fmt.Fprintf(w, "  %s\n", version)
			}
		}
		return nil
	},
}
