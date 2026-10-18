// Command am-autotask is an Alertmanager webhook receiver that raises and
// maintains Autotask tickets, one per alert group.
package main

import (
	"context"
	"os"

	"github.com/andrewheberle/am-autotask/internal/cli"
)

// Version is set at build time.
var Version = "dev"

func main() {
	os.Exit(cli.Execute(context.Background(), os.Args[1:], Version))
}
