// Package cli implements the am-autotask command and its subcommands.
package cli

import (
	"context"
	"errors"

	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
)

// errNoCommand is returned when no subcommand is given, so a container
// started with arguments from before subcommands existed fails visibly.
var errNoCommand = errors.New("a subcommand is required")

type rootCommand struct {
	*simplecommand.Command
}

func (c *rootCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	_ = cd.CobraCommand.Usage()
	return errNoCommand
}

// Execute runs the command named by args (without the program name).
func Execute(ctx context.Context, args []string, version string) error {
	root := &rootCommand{
		Command: simplecommand.New("am-autotask", "Alertmanager webhook receiver that raises Autotask tickets",
			simplecommand.WithVersion(version),
			simplecommand.WithSubCommands(newServeCommand(version)),
		),
	}

	x, err := simplecobra.New(root)
	if err != nil {
		return err
	}
	_, err = x.Execute(ctx, args)
	return err
}
