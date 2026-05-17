package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAvailableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "available",
		Aliases: []string{"list-remote"},
		Short:   "List installable Go versions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := getManager(cmd)
			if err != nil {
				return err
			}
			all, err := cmd.Flags().GetBool("all")
			if err != nil {
				return err
			}

			versions, err := manager.Available(cmd.Context(), all)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			if len(versions) == 0 {
				fmt.Fprintln(w, "No Go versions available for this platform.")
				return nil
			}
			for _, version := range versions {
				fmt.Fprintln(w, version)
			}
			return nil
		},
	}
	cmd.Flags().Bool("all", false, "show all stable patch releases")
	return cmd
}
