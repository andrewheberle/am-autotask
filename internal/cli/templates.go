package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/andrewheberle/am-autotask/internal/config"
	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// templatesCommand prints the ticket templates rendered for a notification.
type templatesCommand struct {
	notification string
	alertGroups  string
	externalURL  string

	*simplecommand.Command
}

func newTemplatesCommand() *templatesCommand {
	return &templatesCommand{
		Command: simplecommand.New("templates", "Preview ticket templates rendered for a sample or given notification",
			simplecommand.WithArgs(cobra.NoArgs),
			simplecommand.WithLong(templatesLong),
			simplecommand.WithExample(templatesExample),
		),
	}
}

func (c *templatesCommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}
	f := cd.CobraCommand.Flags()
	config.AddTemplatesFlags(f)
	f.StringVar(&c.notification, "notification", "", "path to an Alertmanager webhook payload (JSON) to render instead of the built-in sample, or - for standard input")
	f.StringVar(&c.alertGroups, "alert-groups", "", "path to the output of Alertmanager's /api/v2/alerts/groups (JSON) to render each alert group of instead of the built-in sample, or - for standard input")
	f.StringVar(&c.externalURL, "external-url", "", "Alertmanager's external URL for alert groups given by --alert-groups (default the built-in sample's)")
	cd.CobraCommand.MarkFlagsMutuallyExclusive("notification", "alert-groups")
	return nil
}

func (c *templatesCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	cmd := cd.CobraCommand
	if cmd.Flags().Changed("external-url") && c.alertGroups == "" {
		return errors.New("--external-url can only be used with --alert-groups")
	}
	cfg, err := config.TemplatesFromFlags(cmd.Flags())
	if err != nil {
		return err
	}
	tmpl, err := receiver.ParseTemplates(cfg.Templates, time.Local)
	if err != nil {
		return err
	}

	groups, err := c.previewGroups(cmd.InOrStdin())
	if err != nil {
		return err
	}

	return previewTemplates(cmd.OutOrStdout(), tmpl, groups, cfg)
}

// previewGroups returns the notifications to preview: the built-in sample,
// the webhook payload given by --notification, or each alert group given by
// --alert-groups.
func (c *templatesCommand) previewGroups(stdin io.Reader) ([]previewGroup, error) {
	sample, err := receiver.SampleNotification()
	if err != nil {
		return nil, err
	}

	switch {
	case c.notification != "":
		b, name, err := readInput(c.notification, stdin)
		if err != nil {
			return nil, fmt.Errorf("reading notification: %w", err)
		}
		n, err := parseNotification(b, name)
		if err != nil {
			return nil, err
		}
		return []previewGroup{{notification: n}}, nil
	case c.alertGroups != "":
		externalURL := sample.ExternalURL
		if c.externalURL != "" {
			u, err := url.Parse(c.externalURL)
			if err != nil || !u.IsAbs() {
				return nil, fmt.Errorf("--external-url %q is not an absolute URL", c.externalURL)
			}
			externalURL = c.externalURL
		}
		b, name, err := readInput(c.alertGroups, stdin)
		if err != nil {
			return nil, fmt.Errorf("reading alert groups: %w", err)
		}
		return parseAlertGroups(b, name, externalURL)
	default:
		return []previewGroup{{notification: sample}}, nil
	}
}

// readInput reads the file at path, or stdin when path is -, returning its
// contents and a name for it in errors.
func readInput(path string, stdin io.Reader) ([]byte, string, error) {
	if path == "-" {
		b, err := io.ReadAll(stdin)
		return b, "standard input", err
	}
	b, err := os.ReadFile(path)
	return b, path, err
}

// previewTemplates writes each rendered template under a heading, with each
// alert group's under a banner when the groups have headings. A group whose
// templates fail shows the error, and the failures are returned once every
// group is written.
func previewTemplates(w io.Writer, tmpl *receiver.Templates, groups []previewGroup, cfg config.Preview) error {
	var failed []error
	for _, g := range groups {
		if g.heading != "" {
			if _, err := fmt.Fprintf(w, "##### %s\n\n", g.heading); err != nil {
				return err
			}
		}
		if g.skipped {
			if _, err := fmt.Fprint(w, "skipped: every alert is silenced or inhibited\n\n"); err != nil {
				return err
			}
			continue
		}
		out, err := tmpl.Preview(g.notification, cfg.SeverityLabel, cfg.SeverityOrder)
		if err != nil {
			if g.heading != "" {
				err = fmt.Errorf("%s: %w", g.heading, err)
			}
			failed = append(failed, err)
			if _, err := fmt.Fprintf(w, "error: %v\n\n", err); err != nil {
				return err
			}
			continue
		}
		for _, name := range receiver.TemplateNames {
			if _, err := fmt.Fprintf(w, "=== %s\n%s\n\n", name, out[name]); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(w, "System notices, such as raising against the catch-all company, come before descriptions and are not shown."); err != nil {
		return err
	}
	return errors.Join(failed...)
}
