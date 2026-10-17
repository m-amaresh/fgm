package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the active Go version",
		Long: "Show the Go version the shims resolve to from the current directory. " +
			"A .go-version file takes precedence over the globally activated version.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := getManager(cmd)
			if err != nil {
				return err
			}
			effective, err := manager.EffectiveCwd()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if effective.Version == "" {
				fmt.Fprintln(w, "No active Go version. Run: fgm use latest")
				return nil
			}
			if effective.IsLocal() {
				fmt.Fprintf(w, "%s (pinned by %s)\n", effective.Version, effective.Path)
				return nil
			}
			fmt.Fprintln(w, effective.Version)
			return nil
		},
	}
}
