package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(currentCmd)
}

var currentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the active Go version",
	RunE: func(cmd *cobra.Command, _ []string) error {
		manager := getManager(cmd)
		version, err := manager.Current()
		if err != nil {
			return err
		}
		if version == "" {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "no active Go version")
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), version)
		return err
	},
}
