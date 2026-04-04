package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/m-amaresh/fgm/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if err := cli.Execute(ctx); err != nil {
		stop()
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(os.Stderr, "\nOperation canceled.")
			os.Exit(130)
		}
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	stop()
}
