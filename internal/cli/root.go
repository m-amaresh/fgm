package cli

import (
	"fmt"
	"os"

	"github.com/m-amaresh/fgm/internal/fgm"

	"github.com/spf13/cobra"
)

// Set via ldflags at build time.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

var (
	manager *fgm.Manager
	verbose bool
)

const banner = "" +
	"\n███████╗ ██████╗ ███╗   ███╗\n" +
	"██╔════╝██╔════╝ ████╗ ████║\n" +
	"█████╗  ██║  ███╗██╔████╔██║\n" +
	"██╔══╝  ██║   ██║██║╚██╔╝██║\n" +
	"██║     ╚██████╔╝██║ ╚═╝ ██║\n" +
	"╚═╝      ╚═════╝ ╚═╝     ╚═╝"

var rootCmd = &cobra.Command{
	Use:   "fgm",
	Short: "Fast Go Manager – install and switch Go versions in seconds",
	Long:  banner,
}

func init() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose diagnostic output")
}

func Execute() error {
	// Use PersistentPreRunE to initialize the manager after flags are parsed,
	// so the --verbose flag is available.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if manager != nil {
			return nil
		}
		m, err := fgm.NewManager("", stderrLog)
		if err != nil {
			return err
		}
		m.Verbose = verbose
		manager = m
		return nil
	}

	return rootCmd.Execute()
}

// stderrLog writes manager messages to stderr.
func stderrLog(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(os.Stderr, msg)
}
