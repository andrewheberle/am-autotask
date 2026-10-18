// Command am-autotask is an Alertmanager webhook receiver that raises and
// maintains Autotask tickets, one per alert group.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/andrewheberle/am-autotask/internal/cli"
)

// Version is set at build time.
var Version = "dev"

func main() {
	if err := cli.Execute(context.Background(), os.Args[1:], Version); err != nil {
		slog.Error("exiting", "error", err)
		os.Exit(1)
	}
}
