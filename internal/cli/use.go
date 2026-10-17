package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/m-amaresh/fgm/internal/fgm"
)

func newUseCmd() *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "use <version>",
		Short: "Install if needed, then activate a Go version",
		Long: "Install if needed, then activate a Go version. Accepts exact (1.25.5), " +
			"minor (1.25), or \"latest\".\n\n" +
			"With --local the version is pinned for the current directory tree by " +
			"writing " + fgm.LocalVersionFile + ", leaving the global version alone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			manager, err := getManager(cmd)
			if err != nil {
				return err
			}
			version, err := manager.ResolveVersion(ctx, args[0])
			if err != nil {
				return err
			}
			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}
			out := cmd.OutOrStdout()

			if local {
				path, err := manager.UseLocal(ctx, version, dir)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%s Pinned go %s in %s\n", green("✓"), version, path)
				return nil
			}

			if err := manager.Use(ctx, version); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s Using go %s\n", green("✓"), version)

			// A pin in scope silently wins over what was just activated, so
			// say so rather than letting `go version` disagree. An unusable
			// pin is worth the same warning: the shims will refuse it.
			switch pinned, path, err := fgm.FindLocalVersion(dir); {
			case err != nil:
				fmt.Fprintf(out, "%s %v\n", yellow("!"), err)
			case pinned != "" && pinned != version:
				fmt.Fprintf(out, "%s %s pins go %s here, which overrides this\n",
					yellow("!"), path, pinned)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false,
		"pin the version for this directory via "+fgm.LocalVersionFile+" instead of activating it globally")
	return cmd
}
