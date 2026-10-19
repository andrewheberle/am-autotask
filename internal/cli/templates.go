package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	*simplecommand.Command
}

func newTemplatesCommand() *templatesCommand {
	return &templatesCommand{
		Command: simplecommand.New("templates", "Preview ticket templates rendered for a sample or given notification",
			simplecommand.WithArgs(cobra.NoArgs),
			simplecommand.WithExample("  am-autotask templates --templates templates.tmpl --notification webhook.json"),
		),
	}
}

func (c *templatesCommand) Init(cd *simplecobra.Commandeer) error {
	if err := c.Command.Init(cd); err != nil {
		return err
	}
	f := cd.CobraCommand.Flags()
	config.AddTemplatesFlags(f)
	f.StringVar(&c.notification, "notification", "", "path to an Alertmanager webhook payload (JSON) to render instead of the built-in sample")
	return nil
}

func (c *templatesCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	path, err := config.TemplatesFromFlags(cd.CobraCommand.Flags())
	if err != nil {
		return err
	}
	tmpl, err := receiver.ParseTemplates(path, time.Local)
	if err != nil {
		return err
	}

	n, err := receiver.SampleNotification()
	if c.notification != "" {
		n, err = readNotification(c.notification)
	}
	if err != nil {
		return err
	}

	return previewTemplates(cd.CobraCommand.OutOrStdout(), tmpl, n)
}

func readNotification(path string) (receiver.Notification, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return receiver.Notification{}, fmt.Errorf("reading notification: %w", err)
	}
	var n receiver.Notification
	if err := json.Unmarshal(b, &n); err != nil {
		return receiver.Notification{}, fmt.Errorf("parsing notification %s: %w", path, err)
	}
	return n, nil
}

// previewTemplates writes each rendered template under a heading.
func previewTemplates(w io.Writer, tmpl *receiver.Templates, n receiver.Notification) error {
	out, err := tmpl.Preview(n)
	if err != nil {
		return err
	}
	for _, name := range receiver.TemplateNames {
		if _, err := fmt.Fprintf(w, "=== %s\n%s\n\n", name, out[name]); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(w, "System notices, such as raising against the catch-all company, come before descriptions and are not shown.")
	return err
}
