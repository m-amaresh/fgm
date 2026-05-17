package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the active Go version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := getManager(cmd)
			if err != nil {
				return err
			}
			version, err := manager.Current()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if version == "" {
				fmt.Fprintln(w, "no active Go version")
				return nil
			}
			fmt.Fprintln(w, version)
			return nil
		},
	}
}
