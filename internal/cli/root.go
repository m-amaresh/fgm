package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/m-amaresh/fgm/internal/fgm"
)

// Set via ldflags at build time.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type managerKey struct{}

// getManager retrieves the Manager from the cobra command context.
func getManager(cmd *cobra.Command) (*fgm.Manager, error) {
	m, ok := cmd.Context().Value(managerKey{}).(*fgm.Manager)
	if !ok {
		return nil, errors.New("internal: manager not initialized")
	}
	return m, nil
}

const banner = "" +
	"\n███████╗ ██████╗ ███╗   ███╗\n" +
	"██╔════╝██╔════╝ ████╗ ████║\n" +
	"█████╗  ██║  ███╗██╔████╔██║\n" +
	"██╔══╝  ██║   ██║██║╚██╔╝██║\n" +
	"██║     ╚██████╔╝██║ ╚═╝ ██║\n" +
	"╚═╝      ╚═════╝ ╚═╝     ╚═╝"

func init() {
	cobra.EnableTraverseRunHooks = true
}

// NewRootCmd builds a fresh root command tree with all subcommands attached.
// Each call returns an independent tree, which is essential for tests that
// need isolated flag state.
func NewRootCmd() *cobra.Command {
	var verbose bool
	root := &cobra.Command{
		Use:           "fgm",
		Short:         "Fast Go Manager – install and switch Go versions in seconds",
		Long:          banner,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose diagnostic output")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		m, err := fgm.NewManager("", stderrLog)
		if err != nil {
			return err
		}
		m.Verbose = verbose
		cmd.SetContext(context.WithValue(cmd.Context(), managerKey{}, m))
		return nil
	}

	root.AddCommand(
		newAvailableCmd(),
		newCurrentCmd(),
		newDoctorCmd(),
		newEnvCmd(),
		newInstallCmd(),
		newListCmd(),
		newPruneCmd(),
		newUninstallCmd(),
		newUseCmd(),
		newVersionCmd(),
	)
	return root
}

func Execute(ctx context.Context) error {
	return NewRootCmd().ExecuteContext(ctx)
}

// stderrLog writes manager messages to stderr.
func stderrLog(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(os.Stderr, msg)
}
