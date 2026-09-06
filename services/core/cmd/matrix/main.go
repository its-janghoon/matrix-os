// Command matrix is the command-line operations tool for a Matrix OS node. It
// is a thin entrypoint that delegates all command logic to internal/cli (so the
// command tree is unit-testable in-process). Run `matrix --help` for usage.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ecirlabs/matrix-core/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCommand()
	if err := root.ExecuteContext(ctx); err != nil {
		// cobra already prints the error (SilenceErrors is false); exit non-zero.
		os.Exit(1)
	}
}
