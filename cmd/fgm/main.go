package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/m-amaresh/fgm/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	if err := cli.Execute(ctx); err != nil {
		stop()
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stop()
}
