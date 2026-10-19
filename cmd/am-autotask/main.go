// Command am-autotask is an Alertmanager webhook receiver that raises and
// maintains Autotask tickets, one per alert group.
package main

import (
	"context"
	"os"
	// time zones for TZ on hosts without zoneinfo
	_ "time/tzdata"

	"github.com/andrewheberle/am-autotask/internal/cli"
)

// Version is set at build time.
var Version = "dev"

func main() {
	os.Exit(cli.Execute(context.Background(), os.Args[1:], Version))
}
