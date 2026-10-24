package cli

import (
	"context"
	"io"

	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/andrewheberle/am-autotask/internal/autotask"
	"github.com/andrewheberle/am-autotask/internal/config"
	"github.com/andrewheberle/am-autotask/internal/lookup"
)

// lookupCommand groups the commands that print Autotask IDs for settings.
// Its Autotask flags are inherited by its subcommands.
type lookupCommand struct {
	*simplecommand.Command
}

func newLookupCommand() *lookupCommand {
	return &lookupCommand{
		Command: simplecommand.New("lookup", "Print the Autotask IDs that settings take",
			simplecommand.WithSubCommands(
				&lookupSubcommand{
					Command: simplecommand.New("picklists", "Print the IDs of every picklist setting",
						simplecommand.WithArgs(cobra.NoArgs),
					),
					allUsage: "include inactive values",
					run: func(ctx context.Context, w io.Writer, at lookup.Autotask, all bool, _ []string) error {
						return lookup.Picklists(ctx, w, at, all)
					},
				},
				&lookupSubcommand{
					Command: simplecommand.New("companies", "Print the IDs of companies whose name contains NAME",
						simplecommand.WithArgs(cobra.ExactArgs(1)),
						simplecommand.WithExample("  am-autotask lookup companies --config config.yaml \"R-Group\""),
					),
					allUsage: "include inactive companies",
					run: func(ctx context.Context, w io.Writer, at lookup.Autotask, all bool, args []string) error {
						return lookup.Companies(ctx, w, at, args[0], all)
					},
				},
				&lookupSubcommand{
					Command: simplecommand.New("worktypes", "Print the IDs of work types, for ticket.worktype",
						simplecommand.WithArgs(cobra.NoArgs),
					),
					allUsage: "include inactive work types",
					run: func(ctx context.Context, w io.Writer, at lookup.Autotask, all bool, _ []string) error {
						return lookup.WorkTypes(ctx, w, at, all)
					},
				},
			),
		),
	}
}

func (c *lookupCommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}
	config.AddAutotaskFlags(cd.CobraCommand.PersistentFlags())
	return nil
}

func (c *lookupCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	_ = cd.CobraCommand.Usage()
	return errNoCommand
}

type lookupSubcommand struct {
	all      bool
	allUsage string
	run      func(ctx context.Context, w io.Writer, at lookup.Autotask, all bool, args []string) error

	*simplecommand.Command
}

func (c *lookupSubcommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}
	cd.CobraCommand.Flags().BoolVar(&c.all, "all", false, c.allUsage)
	return nil
}

func (c *lookupSubcommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	cfg, err := config.AutotaskFromFlags(cd.CobraCommand.Flags())
	if err != nil {
		return err
	}

	client, err := autotask.New(ctx, autotask.Options{
		Username:           cfg.Username,
		Secret:             cfg.Secret,
		IntegrationCode:    cfg.IntegrationCode,
		ZoneInformationURL: cfg.ZoneURL,
	})
	if err != nil {
		return err
	}
	return c.run(ctx, cd.CobraCommand.OutOrStdout(), client, c.all, args)
}
