// Package config loads the receiver's configuration from command line flags,
// a YAML configuration file and AM_AUTOTASK_ prefixed environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
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

	// TracingExporter is none, otlp or console; everything else about
	// tracing comes from OTEL_* environment variables (ADR-0003).
	TracingExporter string

	Autotask Autotask

	CompanySelectorsUDF string
	RecordedAlertsUDF   string
	// AppliedPriorityUDF is empty unless ticket priority follows severity.
	AppliedPriorityUDF string
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
var (
	autotaskSecrets = []string{"autotask.username", "autotask.secret", "autotask.integrationcode"}
	secrets         = append([]string{"webhook.token"}, autotaskSecrets...)
)

// AddAutotaskFlags adds the flags for the configuration file and the Autotask
// API settings to f, for commands that only talk to Autotask.
func AddAutotaskFlags(f *pflag.FlagSet) {
	f.String("config", "", "path to YAML configuration file")

	f.String("autotask.username", "", "Autotask API user name (required)")
	f.String("autotask.usernamefile", "", "file containing autotask.username")
	f.String("autotask.secret", "", "Autotask API secret (required)")
	f.String("autotask.secretfile", "", "file containing autotask.secret")
	f.String("autotask.integrationcode", "", "Autotask API tracking identifier (required)")
	f.String("autotask.integrationcodefile", "", "file containing autotask.integrationcode")
	f.String("autotask.zoneurl", autotask.DefaultZoneInformationURL, "Autotask zone information URL")
}

// AddFlags adds a command line flag for every setting to f.
func AddFlags(f *pflag.FlagSet) {
	AddAutotaskFlags(f)
	f.String("listen", ":8080", "address to listen on")
	f.String("log.level", "info", "log level (debug, info, warn, error)")
	f.String("tracing.exporter", "none", "trace exporter: none, otlp (OTLP over HTTP, configured by OTEL_* variables) or console")

	f.String("webhook.token", "", "bearer token Alertmanager must send (required)")
	f.String("webhook.tokenfile", "", "file containing webhook.token")

	f.Var(&optionalID{}, "company.catchall", "ID of the catch-all company, which may be 0 for your own company (required)")
	f.String("company.udf", "Alertmanager Selectors", "name of the Company UDF holding company selectors")
	f.Duration("company.refresh", 15*time.Minute, "how often company selectors are reloaded")
	f.Duration("company.missrefresh", time.Minute, "minimum time between reloads when an alert group matches no company")

	f.String("ticket.udf", "Alertmanager Recorded Alerts", "name of the Ticket UDF holding recorded alerts")
	f.StringToInt("ticket.priorities", map[string]int{}, "severity to ticket priority ID map, e.g. critical=4,warning=2")
	f.Int("ticket.defaultpriority", 0, "ticket priority ID for severities not in ticket.priorities (required)")
	f.String("ticket.severitylabel", "severity", "label whose value is mapped through ticket.priorities")
	f.StringSlice("ticket.severityorder", nil, "severities in ticket.priorities, most serious first; when set, an alert group's priority comes from its highest firing severity (optional)")
	f.String("ticket.appliedpriorityudf", "", "name of the Ticket UDF holding the priority last applied; when set, ticket priority follows severity (optional, requires ticket.severityorder)")
	f.Int("ticket.status", 1, "status ID of new tickets")
	f.Int("ticket.type", 5, "ticket type ID of new tickets")
	f.Int("ticket.source", 8, "source ID of new tickets")
	f.Int("ticket.queue", 0, "queue ID of new tickets (optional)")
	f.Int("ticket.issuetype", 0, "issue type ID of new tickets (optional)")
	f.Int("ticket.subissuetype", 0, "sub-issue type ID of new tickets (optional)")
	f.Int("ticket.category", 0, "ticket category ID of new tickets (optional)")
	f.Int("ticket.worktype", 0, "work type (billing code) ID of new tickets (optional)")
	f.Duration("ticket.duein", 0, "due date of new tickets relative to creation (optional)")
	f.Int("ticket.resolvedstatus", 5, "status ID set when the whole alert group resolves; 0 leaves the status unchanged")

	f.Int("note.type", 1, "note type ID of ticket notes")
	f.Int("note.publish", 2, "publish (visibility) ID of ticket notes")
}

// FromFlags loads the configuration from f, which holds the flags added by
// [AddFlags] already parsed, with unset flags taken from environment variables
// and then the configuration file.
func FromFlags(f *pflag.FlagSet) (Config, error) {
	k, err := configger.LoadConfig(f, configger.WithEnvPrefix(EnvPrefix))
	if err != nil {
		return Config{}, err
	}

	values, errs := requiredSecrets(k, secrets)

	var level slog.Level
	if err := level.UnmarshalText([]byte(k.String("log.level"))); err != nil {
		errs = append(errs, fmt.Errorf("log.level: %w", err))
	}

	cfg := Config{
		Listen:          k.String("listen"),
		LogLevel:        level,
		WebhookToken:    values["webhook.token"],
		TracingExporter: k.String("tracing.exporter"),
		Autotask: Autotask{
			Username:        values["autotask.username"],
			Secret:          values["autotask.secret"],
			IntegrationCode: values["autotask.integrationcode"],
			ZoneURL:         k.String("autotask.zoneurl"),
		},
		CompanySelectorsUDF: k.String("company.udf"),
		RecordedAlertsUDF:   k.String("ticket.udf"),
		AppliedPriorityUDF:  k.String("ticket.appliedpriorityudf"),
		Refresh:             k.Duration("company.refresh"),
		MissRefresh:         k.Duration("company.missrefresh"),
		Receiver: receiver.Options{
			CatchAllCompanyID: k.Int64("company.catchall"),
			SeverityLabel:     k.String("ticket.severitylabel"),
			Priorities:        k.IntMap("ticket.priorities"),
			DefaultPriority:   k.Int("ticket.defaultpriority"),
			SeverityOrder:     k.Strings("ticket.severityorder"),
			FollowSeverity:    k.String("ticket.appliedpriorityudf") != "",
			Status:            k.Int("ticket.status"),
			TicketType:        k.Int("ticket.type"),
			Source:            k.Int("ticket.source"),
			QueueID:           k.Int("ticket.queue"),
			IssueType:         k.Int("ticket.issuetype"),
			SubIssueType:      k.Int("ticket.subissuetype"),
			Category:          k.Int("ticket.category"),
			WorkType:          k.Int("ticket.worktype"),
			DueIn:             k.Duration("ticket.duein"),
			ResolvedStatus:    k.Int("ticket.resolvedstatus"),
			NoteType:          k.Int("note.type"),
			NotePublish:       k.Int("note.publish"),
		},
	}

	// company 0 is the instance's own company, so only an unset value is
	// missing; the flag is empty unless given
	if v := k.String("company.catchall"); v == "" {
		errs = append(errs, errors.New("company.catchall is required"))
	} else if _, err := strconv.ParseInt(v, 10, 64); err != nil {
		errs = append(errs, fmt.Errorf("company.catchall %q is not an ID", v))
	}

	required := []struct {
		key string
		set bool
	}{
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
	errs = append(errs, severityOrderErrors(cfg.Receiver)...)
	switch cfg.TracingExporter {
	case "none", "otlp", "console":
	default:
		errs = append(errs, fmt.Errorf("tracing.exporter %q must be none, otlp or console", cfg.TracingExporter))
	}
	if cfg.Refresh <= 0 {
		errs = append(errs, errors.New("company.refresh must be positive"))
	}

	if len(errs) > 0 {
		return Config{}, &invalidError{errs}
	}
	return cfg, nil
}

// severityOrderErrors checks the severity order ranks exactly the severities
// mapped to priorities, and is set when priority follows severity.
func severityOrderErrors(r receiver.Options) []error {
	if len(r.SeverityOrder) == 0 {
		if r.FollowSeverity {
			return []error{errors.New("ticket.appliedpriorityudf requires ticket.severityorder")}
		}
		return nil
	}

	var errs []error
	seen := map[string]bool{}
	for _, s := range r.SeverityOrder {
		switch _, mapped := r.Priorities[s]; {
		case seen[s]:
			errs = append(errs, fmt.Errorf("ticket.severityorder lists %q more than once", s))
		case !mapped:
			errs = append(errs, fmt.Errorf("ticket.severityorder lists %q, which is not in ticket.priorities", s))
		}
		seen[s] = true
	}

	missing := make([]string, 0, len(r.Priorities))
	for s := range r.Priorities {
		if !seen[s] {
			missing = append(missing, s)
		}
	}
	slices.Sort(missing)
	for _, s := range missing {
		errs = append(errs, fmt.Errorf("ticket.severityorder must list %q from ticket.priorities", s))
	}
	return errs
}

// AutotaskFromFlags loads only the Autotask API settings from f, which holds
// the flags added by [AddAutotaskFlags] already parsed, in the same way as
// [FromFlags]. Other settings in the configuration file are ignored.
func AutotaskFromFlags(f *pflag.FlagSet) (Autotask, error) {
	k, err := configger.LoadConfig(f, configger.WithEnvPrefix(EnvPrefix))
	if err != nil {
		return Autotask{}, err
	}

	values, errs := requiredSecrets(k, autotaskSecrets)
	if len(errs) > 0 {
		return Autotask{}, &invalidError{errs}
	}
	return Autotask{
		Username:        values["autotask.username"],
		Secret:          values["autotask.secret"],
		IntegrationCode: values["autotask.integrationcode"],
		ZoneURL:         k.String("autotask.zoneurl"),
	}, nil
}

// requiredSecrets returns the value of each key, with an error for each that
// is unset or can't be read.
func requiredSecrets(k configger.Config, keys []string) (map[string]string, []error) {
	var errs []error
	values := map[string]string{}
	for _, key := range keys {
		v, err := secret(k, key)
		switch {
		case err != nil:
			errs = append(errs, err)
		case v == "":
			errs = append(errs, fmt.Errorf("%s (or %sfile) is required", key, key))
		}
		values[key] = v
	}
	return values, errs
}

// optionalID is an ID flag that is empty until set, so that unset can be told
// apart from 0.
type optionalID struct {
	value *int64
}

func (o *optionalID) String() string {
	if o.value == nil {
		return ""
	}
	return strconv.FormatInt(*o.value, 10)
}

func (o *optionalID) Set(s string) error {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	o.value = &v
	return nil
}

// Type is not "int64", which configger would read as 0 when unset.
func (o *optionalID) Type() string {
	return "id"
}

// invalidError lists every problem with the configuration, one per line.
type invalidError struct {
	errs []error
}

func (e *invalidError) Error() string {
	var b strings.Builder
	b.WriteString("invalid configuration:")
	for _, err := range e.errs {
		b.WriteString("\n  - ")
		b.WriteString(err.Error())
	}
	return b.String()
}

func (e *invalidError) Unwrap() []error {
	return e.errs
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
