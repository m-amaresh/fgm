package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "Print fgm environment diagnostics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := getManager(cmd)
			if err != nil {
				return err
			}

			info := manager.Env()
			current := info.CurrentVersion
			if current == "" {
				current = "none"
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "FGM_DIR:       %s\n", info.FGMDir)
			fmt.Fprintf(w, "shim dir:      %s\n", info.ShimDir)
			fmt.Fprintf(w, "versions dir:  %s\n", info.VersionsDir)
			fmt.Fprintf(w, "downloads dir: %s\n", info.DownloadsDir)
			fmt.Fprintf(w, "current:       %s\n", current)
			fmt.Fprintf(w, "platform:      %s\n", info.Platform)
			if info.CurrentError != "" {
				fmt.Fprintln(w)
				fmt.Fprintf(w, "warning: %s\n", info.CurrentError)
			}
			return nil
		},
	}
}
