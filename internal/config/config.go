// Package config loads the receiver's configuration from command line flags,
// a YAML configuration file and AM_AUTOTASK_ prefixed environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/andrewheberle/configger"
	"github.com/spf13/pflag"

	"github.com/andrewheberle/am-autotask/internal/autotask"
	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// EnvPrefix prefixes environment variables, e.g. AM_AUTOTASK_WEBHOOK_TOKEN
// sets webhook.token.
const EnvPrefix = "AM_AUTOTASK"

// Config is the validated configuration.
type Config struct {
	Listen       string
	LogLevel     slog.Level
	WebhookToken string

	Autotask Autotask

	CompanySelectorsUDF string
	RecordedAlertsUDF   string
	Refresh             time.Duration
	MissRefresh         time.Duration

	// Receiver holds ticket and note settings; Now and Logger are unset.
	Receiver receiver.Options
}

// Autotask holds the Autotask API settings.
type Autotask struct {
	Username        string
	Secret          string
	IntegrationCode string
	ZoneURL         string
}

// secrets may be given directly or, as "<key>file", as a file path
var secrets = []string{"webhook.token", "autotask.username", "autotask.secret", "autotask.integrationcode"}

func flags(name string) *pflag.FlagSet {
	f := pflag.NewFlagSet(name, pflag.ContinueOnError)
	// the caller prints usage, with the version, on pflag.ErrHelp
	f.Usage = func() {}

	f.String("config", "", "path to YAML configuration file")
	f.String("listen", ":8080", "address to listen on")
	f.String("log.level", "info", "log level (debug, info, warn, error)")

	f.String("webhook.token", "", "bearer token Alertmanager must send (required)")
	f.String("webhook.tokenfile", "", "file containing webhook.token")

	f.String("autotask.username", "", "Autotask API user name (required)")
	f.String("autotask.usernamefile", "", "file containing autotask.username")
	f.String("autotask.secret", "", "Autotask API secret (required)")
	f.String("autotask.secretfile", "", "file containing autotask.secret")
	f.String("autotask.integrationcode", "", "Autotask API tracking identifier (required)")
	f.String("autotask.integrationcodefile", "", "file containing autotask.integrationcode")
	f.String("autotask.zoneurl", autotask.DefaultZoneInformationURL, "Autotask zone information URL")

	f.Int64("company.catchall", 0, "ID of the catch-all company (required)")
	f.String("company.udf", "Alertmanager Selectors", "name of the Company UDF holding company selectors")
	f.Duration("company.refresh", 15*time.Minute, "how often company selectors are reloaded")
	f.Duration("company.missrefresh", time.Minute, "minimum time between reloads when an alert group matches no company")

	f.String("ticket.udf", "Alertmanager Recorded Alerts", "name of the Ticket UDF holding recorded alerts")
	f.StringToInt("ticket.priorities", map[string]int{}, "severity to ticket priority ID map, e.g. critical=4,warning=2")
	f.Int("ticket.defaultpriority", 0, "ticket priority ID for severities not in ticket.priorities (required)")
	f.String("ticket.severitylabel", "severity", "common label mapped through ticket.priorities")
	f.Int("ticket.status", 1, "status ID of new tickets")
	f.Int("ticket.type", 5, "ticket type ID of new tickets")
	f.Int("ticket.source", 8, "source ID of new tickets")
	f.Int("ticket.queue", 0, "queue ID of new tickets (optional)")
	f.Int("ticket.issuetype", 0, "issue type ID of new tickets (optional)")
	f.Int("ticket.subissuetype", 0, "sub-issue type ID of new tickets (optional)")
	f.Int("ticket.category", 0, "ticket category ID of new tickets (optional)")
	f.Duration("ticket.duein", 0, "due date of new tickets relative to creation (optional)")
	f.Int("ticket.resolvedstatus", 5, "status ID set when the whole alert group resolves; 0 leaves the status unchanged")

	f.Int("note.type", 1, "note type ID of ticket notes")
	f.Int("note.publish", 2, "publish (visibility) ID of ticket notes")

	return f
}

// Usage returns the flag help text.
func Usage() string {
	return flags("am-autotask").FlagUsages()
}

// Load parses args (without the program name) and loads the configuration.
// It returns [pflag.ErrHelp] if help was requested.
func Load(args []string) (Config, error) {
	f := flags("am-autotask")
	if err := f.Parse(args); err != nil {
		return Config{}, err
	}

	k, err := configger.LoadConfig(f, configger.WithEnvPrefix(EnvPrefix))
	if err != nil {
		return Config{}, err
	}

	var errs []error
	values := map[string]string{}
	for _, key := range secrets {
		v, err := secret(k, key)
		switch {
		case err != nil:
			errs = append(errs, err)
		case v == "":
			errs = append(errs, fmt.Errorf("%s (or %sfile) is required", key, key))
		}
		values[key] = v
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(k.String("log.level"))); err != nil {
		errs = append(errs, fmt.Errorf("log.level: %w", err))
	}

	cfg := Config{
		Listen:       k.String("listen"),
		LogLevel:     level,
		WebhookToken: values["webhook.token"],
		Autotask: Autotask{
			Username:        values["autotask.username"],
			Secret:          values["autotask.secret"],
			IntegrationCode: values["autotask.integrationcode"],
			ZoneURL:         k.String("autotask.zoneurl"),
		},
		CompanySelectorsUDF: k.String("company.udf"),
		RecordedAlertsUDF:   k.String("ticket.udf"),
		Refresh:             k.Duration("company.refresh"),
		MissRefresh:         k.Duration("company.missrefresh"),
		Receiver: receiver.Options{
			CatchAllCompanyID: k.Int64("company.catchall"),
			SeverityLabel:     k.String("ticket.severitylabel"),
			Priorities:        k.IntMap("ticket.priorities"),
			DefaultPriority:   k.Int("ticket.defaultpriority"),
			Status:            k.Int("ticket.status"),
			TicketType:        k.Int("ticket.type"),
			Source:            k.Int("ticket.source"),
			QueueID:           k.Int("ticket.queue"),
			IssueType:         k.Int("ticket.issuetype"),
			SubIssueType:      k.Int("ticket.subissuetype"),
			Category:          k.Int("ticket.category"),
			DueIn:             k.Duration("ticket.duein"),
			ResolvedStatus:    k.Int("ticket.resolvedstatus"),
			NoteType:          k.Int("note.type"),
			NotePublish:       k.Int("note.publish"),
		},
	}

	required := []struct {
		key string
		set bool
	}{
		{"company.catchall", cfg.Receiver.CatchAllCompanyID != 0},
		{"ticket.defaultpriority", cfg.Receiver.DefaultPriority != 0},
		{"ticket.status", cfg.Receiver.Status != 0},
		{"note.type", cfg.Receiver.NoteType != 0},
		{"note.publish", cfg.Receiver.NotePublish != 0},
		{"company.udf", cfg.CompanySelectorsUDF != ""},
		{"ticket.udf", cfg.RecordedAlertsUDF != ""},
	}
	for _, r := range required {
		if !r.set {
			errs = append(errs, fmt.Errorf("%s is required", r.key))
		}
	}
	if cfg.Refresh <= 0 {
		errs = append(errs, errors.New("company.refresh must be positive"))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, nil
}

// secret returns the value of key, read from the file named by "<key>file"
// if that is set instead.
func secret(k configger.Config, key string) (string, error) {
	value, path := k.String(key), k.String(key+"file")
	switch {
	case value != "" && path != "":
		return "", fmt.Errorf("%s: set either %s or %sfile, not both", key, key, key)
	case path == "":
		return value, nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// PicklistChecks returns a check for every configured picklist ID so they
// can be verified against Autotask at startup.
func (c Config) PicklistChecks() []autotask.PicklistCheck {
	r := c.Receiver
	checks := []autotask.PicklistCheck{
		{Entity: "Tickets", Field: "priority", Value: r.DefaultPriority, Setting: "ticket.defaultpriority"},
	}

	severities := make([]string, 0, len(r.Priorities))
	for s := range r.Priorities {
		severities = append(severities, s)
	}
	slices.Sort(severities)
	for _, s := range severities {
		checks = append(checks, autotask.PicklistCheck{Entity: "Tickets", Field: "priority", Value: r.Priorities[s], Setting: "ticket.priorities." + s})
	}

	for _, opt := range []struct {
		entity, field, setting string
		value                  int
	}{
		{"Tickets", "status", "ticket.status", r.Status},
		{"Tickets", "status", "ticket.resolvedstatus", r.ResolvedStatus},
		{"Tickets", "ticketType", "ticket.type", r.TicketType},
		{"Tickets", "source", "ticket.source", r.Source},
		{"Tickets", "queueID", "ticket.queue", r.QueueID},
		{"Tickets", "issueType", "ticket.issuetype", r.IssueType},
		{"Tickets", "subIssueType", "ticket.subissuetype", r.SubIssueType},
		{"Tickets", "ticketCategory", "ticket.category", r.Category},
		{"TicketNotes", "noteType", "note.type", r.NoteType},
		{"TicketNotes", "publish", "note.publish", r.NotePublish},
	} {
		if opt.value != 0 {
			checks = append(checks, autotask.PicklistCheck{Entity: opt.entity, Field: opt.field, Value: opt.value, Setting: opt.setting})
		}
	}

	return checks
}
