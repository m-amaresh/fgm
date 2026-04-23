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

var verbose bool

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

var rootCmd = &cobra.Command{
	Use:           "fgm",
	Short:         "Fast Go Manager – install and switch Go versions in seconds",
	Long:          banner,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	cobra.EnableTraverseRunHooks = true
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose diagnostic output")
}

func Execute(ctx context.Context) error {
	// Use PersistentPreRunE to initialize the manager after flags are parsed,
	// so the --verbose flag is available.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		m, err := fgm.NewManager("", stderrLog)
		if err != nil {
			return err
		}
		m.Verbose = verbose
		cmd.SetContext(context.WithValue(cmd.Context(), managerKey{}, m))
		return nil
	}

	return rootCmd.ExecuteContext(ctx)
}

// stderrLog writes manager messages to stderr.
func stderrLog(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(os.Stderr, msg)
}
