// Package cli implements the am-autotask command and its subcommands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

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

// reportedError is an error that has already been logged, so is not printed
// again on exit.
type reportedError struct {
	error
}

func (e *reportedError) Unwrap() error {
	return e.error
}

// Execute runs the command named by args (without the program name), printing
// any error to stderr, and returns the exit code.
func Execute(ctx context.Context, args []string, version string) int {
	return report(os.Stderr, execute(ctx, args, version))
}

// report prints err to w, unless it has already been logged, and returns the
// exit code for it.
func report(w io.Writer, err error) int {
	if err == nil {
		return 0
	}
	// simplecobra prefixes errors parsing the command line with "command error"
	var cmdErr *simplecobra.CommandError
	if errors.As(err, &cmdErr) {
		err = cmdErr.Err
	}
	var reported *reportedError
	if !errors.As(err, &reported) {
		_, _ = fmt.Fprintf(w, "Error: %v\n", err)
	}
	return 1
}

func execute(ctx context.Context, args []string, version string) error {
	root := &rootCommand{
		Command: simplecommand.New("am-autotask", "Alertmanager webhook receiver that raises Autotask tickets",
			simplecommand.WithVersion(version),
			simplecommand.WithSubCommands(newServeCommand(version), newLookupCommand(), newTemplatesCommand()),
		),
	}

	x, err := simplecobra.New(root)
	if err != nil {
		return err
	}
	_, err = x.Execute(ctx, args)
	return err
}
