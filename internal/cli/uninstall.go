package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/m-amaresh/fgm/internal/fgm"
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "uninstall <version>",
		Aliases: []string{"un"},
		Short:   "Remove an installed Go version",
		Long:    "Remove an installed Go version. Accepts exact (1.25.5), minor (1.25), or \"latest\" from locally installed versions.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, err := getManager(cmd)
			if err != nil {
				return err
			}
			version, err := manager.ResolveInstalledVersion(args[0])
			if err != nil {
				return err
			}
			if err := manager.Uninstall(version); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s Uninstalled go %s\n", green("✓"), version)

			// A .go-version naming the version just removed keeps pointing at
			// it, and fgm cannot edit a project's file on the user's behalf —
			// so say where the now-broken pin is.
			if dir, err := os.Getwd(); err == nil {
				if pinned, path, err := fgm.FindLocalVersion(dir); err == nil && pinned == version {
					fmt.Fprintf(out, "%s %s still pins go %s here; update it or run: fgm use <version> --local\n",
						yellow("!"), path, version)
				}
			}
			return nil
		},
	}
}
