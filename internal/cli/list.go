package cli

import (
	"fmt"

	"github.com/fatih/color"
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
		manager := getManager(cmd)
		versions, current, err := manager.List()
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "No Go versions installed. Run: fgm install latest")
			return err
		}
		w := cmd.OutOrStdout()
		for _, version := range versions {
			if version == current {
				green := color.New(color.Bold, color.FgGreen).SprintFunc()
				if _, err := fmt.Fprintf(w, "%s (current)\n", green("* "+version)); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintf(w, "  %s\n", version); err != nil {
					return err
				}
			}
		}
		return nil
	},
}
