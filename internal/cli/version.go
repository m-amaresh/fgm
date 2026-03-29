package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the fgm version",
	RunE: func(cmd *cobra.Command, args []string) error {
		w := cmd.OutOrStdout()
		if _, err := fmt.Fprintf(w, "version: %s\n", Version); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "commit:  %s\n", Commit); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "built:   %s\n", Date); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "go:      %s\n", runtime.Version()); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH); err != nil {
			return err
		}
		return nil
	},
}
