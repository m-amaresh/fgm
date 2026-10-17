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
			if info.LocalVersion != "" {
				fmt.Fprintf(w, "local:         %s (%s)\n", info.LocalVersion, info.LocalFile)
			}
			fmt.Fprintf(w, "platform:      %s\n", info.Platform)
			fmt.Fprintf(w, "GOTOOLCHAIN:   %s\n", info.GoToolchain)
			for _, warning := range []string{info.LocalError, info.CurrentError} {
				if warning != "" {
					fmt.Fprintln(w)
					fmt.Fprintf(w, "warning: %s\n", warning)
				}
			}
			return nil
		},
	}
}
