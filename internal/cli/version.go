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
	RunE: func(cmd *cobra.Command, _ []string) error {
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "version: %s\n", Version)
		fmt.Fprintf(w, "commit:  %s\n", Commit)
		fmt.Fprintf(w, "built:   %s\n", Date)
		fmt.Fprintf(w, "go:      %s\n", runtime.Version())
		fmt.Fprintf(w, "os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}
