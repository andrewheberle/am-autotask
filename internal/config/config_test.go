package config_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/andrewheberle/am-autotask/internal/autotask"
	"github.com/andrewheberle/am-autotask/internal/config"
)

// required settings other than the ticket block
const baseYAML = `
webhook:
  token: hook-token
autotask:
  username: api@example.com
  secret: s3cret
  integrationcode: CODE
company:
  catchall: 175
`

// required settings only
const minimalYAML = baseYAML + `
ticket:
  defaultpriority: 3
`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// loadArgs parses args as a command would, then loads the configuration.
func loadArgs(args []string) (config.Config, error) {
	f := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	config.AddFlags(f)
	if err := f.Parse(args); err != nil {
		return config.Config{}, err
	}
	return config.FromFlags(f)
}

func load(t *testing.T, args ...string) config.Config {
	t.Helper()
	cfg, err := loadArgs(args)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestDefaults(t *testing.T) {
	cfg := load(t, "--config", writeFile(t, "c.yaml", minimalYAML))

	if cfg.Listen != ":8080" {
		t.Errorf("Listen = %q, want :8080", cfg.Listen)
	}
	if cfg.CompanySelectorsUDF != "Alertmanager Selectors" || cfg.RecordedAlertsUDF != "Alertmanager Recorded Alerts" {
		t.Errorf("UDF names = %q, %q", cfg.CompanySelectorsUDF, cfg.RecordedAlertsUDF)
	}
	if cfg.Refresh != 15*time.Minute || cfg.MissRefresh != time.Minute {
		t.Errorf("Refresh, MissRefresh = %v, %v; want 15m, 1m", cfg.Refresh, cfg.MissRefresh)
	}
	r := cfg.Receiver
	if r.Status != 1 || r.TicketType != 5 || r.Source != 8 || r.ResolvedStatus != 5 || r.NoteType != 1 || r.NotePublish != 2 {
		t.Errorf("status/type/source/resolved/notetype/publish = %d/%d/%d/%d/%d/%d, want 1/5/8/5/1/2",
			r.Status, r.TicketType, r.Source, r.ResolvedStatus, r.NoteType, r.NotePublish)
	}
	if r.SeverityLabel != "severity" {
		t.Errorf("SeverityLabel = %q, want severity", r.SeverityLabel)
	}
	if r.QueueID != 0 || r.IssueType != 0 || r.SubIssueType != 0 || r.Category != 0 || r.DueIn != 0 {
		t.Errorf("optional ticket fields should default to unset: %+v", r)
	}
}

func TestLoadFromFile(t *testing.T) {
	cfg := load(t, "--config", writeFile(t, "c.yaml", baseYAML+`
listen: ":9095"
ticket:
  defaultpriority: 3
  priorities:
    critical: 4
    warning: 2
  queue: 8
  duein: 4h
  resolvedstatus: 0
note:
  publish: 4
`))

	if cfg.Listen != ":9095" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.WebhookToken != "hook-token" || cfg.Autotask.Username != "api@example.com" || cfg.Autotask.Secret != "s3cret" || cfg.Autotask.IntegrationCode != "CODE" {
		t.Errorf("credentials = %+v, token %q", cfg.Autotask, cfg.WebhookToken)
	}
	r := cfg.Receiver
	if r.CatchAllCompanyID != 175 || r.DefaultPriority != 3 || r.Priorities["critical"] != 4 || r.Priorities["warning"] != 2 {
		t.Errorf("catch-all/priorities = %d/%d/%v", r.CatchAllCompanyID, r.DefaultPriority, r.Priorities)
	}
	if r.QueueID != 8 || r.DueIn != 4*time.Hour || r.ResolvedStatus != 0 || r.NotePublish != 4 {
		t.Errorf("queue/duein/resolved/publish = %d/%v/%d/%d", r.QueueID, r.DueIn, r.ResolvedStatus, r.NotePublish)
	}
}

func TestEnvOverridesFileAndFlagsOverrideEnv(t *testing.T) {
	path := writeFile(t, "c.yaml", minimalYAML+"listen: \":1111\"\n")
	t.Setenv("AM_AUTOTASK_LISTEN", ":2222")
	t.Setenv("AM_AUTOTASK_TICKET_QUEUE", "29682833")
	t.Setenv("AM_AUTOTASK_TICKET_PRIORITIES", "critical=4")

	cfg := load(t, "--config", path, "--ticket.queue", "8")

	if cfg.Listen != ":2222" {
		t.Errorf("Listen = %q, want env value :2222", cfg.Listen)
	}
	if cfg.Receiver.QueueID != 8 {
		t.Errorf("QueueID = %d, want flag value 8", cfg.Receiver.QueueID)
	}
	if cfg.Receiver.Priorities["critical"] != 4 {
		t.Errorf("Priorities = %v, want critical from env", cfg.Receiver.Priorities)
	}
}

// a higher source sets the whole map, as for any other key
func TestPrioritiesFromEnvReplaceThoseInFile(t *testing.T) {
	path := writeFile(t, "c.yaml", baseYAML+`
ticket:
  defaultpriority: 3
  priorities:
    critical: 4
    warning: 2
`)
	t.Setenv("AM_AUTOTASK_TICKET_PRIORITIES", "warning=1,info=3")

	cfg := load(t, "--config", path)

	want := map[string]int{"warning": 1, "info": 3}
	if !maps.Equal(cfg.Receiver.Priorities, want) {
		t.Errorf("Priorities = %v, want %v", cfg.Receiver.Priorities, want)
	}
}

func TestPrioritiesFromFlag(t *testing.T) {
	cfg := load(t, "--config", writeFile(t, "c.yaml", minimalYAML), "--ticket.priorities", "critical=4,warning=2")

	if p := cfg.Receiver.Priorities; p["critical"] != 4 || p["warning"] != 2 || len(p) != 2 {
		t.Errorf("Priorities = %v", p)
	}
}

func TestEverythingFromEnvWithoutFile(t *testing.T) {
	for k, v := range map[string]string{
		"AM_AUTOTASK_WEBHOOK_TOKEN":            "hook-token",
		"AM_AUTOTASK_AUTOTASK_USERNAME":        "api@example.com",
		"AM_AUTOTASK_AUTOTASK_SECRET":          "s3cret",
		"AM_AUTOTASK_AUTOTASK_INTEGRATIONCODE": "CODE",
		"AM_AUTOTASK_COMPANY_CATCHALL":         "175",
		"AM_AUTOTASK_TICKET_DEFAULTPRIORITY":   "3",
	} {
		t.Setenv(k, v)
	}

	cfg := load(t)

	if cfg.Receiver.CatchAllCompanyID != 175 || cfg.Autotask.IntegrationCode != "CODE" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestSecretsFromFiles(t *testing.T) {
	yaml := `
webhook:
  tokenfile: ` + writeFile(t, "token", "hook-token\n") + `
autotask:
  usernamefile: ` + writeFile(t, "user", "  api@example.com\n") + `
  secretfile: ` + writeFile(t, "secret", "s3c,ret\n") + `
  integrationcodefile: ` + writeFile(t, "code", "CODE") + `
company:
  catchall: 175
ticket:
  defaultpriority: 3
`
	cfg := load(t, "--config", writeFile(t, "c.yaml", yaml))

	if cfg.WebhookToken != "hook-token" || cfg.Autotask.Username != "api@example.com" ||
		cfg.Autotask.Secret != "s3c,ret" || cfg.Autotask.IntegrationCode != "CODE" {
		t.Errorf("secrets = %q %+v", cfg.WebhookToken, cfg.Autotask)
	}
}

func TestSecretSetBothWaysIsRejected(t *testing.T) {
	_, err := loadArgs([]string{
		"--config", writeFile(t, "c.yaml", minimalYAML),
		"--webhook.tokenfile", writeFile(t, "token", "other"),
	})

	if err == nil || !strings.Contains(err.Error(), "webhook.token") {
		t.Errorf("err = %v, want one naming webhook.token", err)
	}
}

func TestSecretWithCommaFromEnvIsKeptIntact(t *testing.T) {
	t.Setenv("AM_AUTOTASK_AUTOTASK_SECRET", "abc,def")

	cfg := load(t, "--config", writeFile(t, "c.yaml", strings.Replace(minimalYAML, "  secret: s3cret\n", "", 1)))

	if cfg.Autotask.Secret != "abc,def" {
		t.Errorf("Secret = %q, want %q", cfg.Autotask.Secret, "abc,def")
	}
}

func TestMissingRequiredSettingsAreAllReported(t *testing.T) {
	_, err := loadArgs(nil)
	if err == nil {
		t.Fatal("expected an error")
	}

	for _, key := range []string{"webhook.token", "autotask.username", "autotask.secret", "autotask.integrationcode", "company.catchall", "ticket.defaultpriority"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error should mention %s: %v", key, err)
		}
	}
}

func TestPicklistChecksCoverEveryConfiguredID(t *testing.T) {
	cfg := load(t, "--config", writeFile(t, "c.yaml", baseYAML+`
ticket:
  defaultpriority: 3
  priorities:
    critical: 4
  queue: 8
`))

	got := cfg.PicklistChecks()

	for _, want := range []autotask.PicklistCheck{
		{Entity: "Tickets", Field: "priority", Value: 3, Setting: "ticket.defaultpriority"},
		{Entity: "Tickets", Field: "priority", Value: 4, Setting: "ticket.priorities.critical"},
		{Entity: "Tickets", Field: "status", Value: 1, Setting: "ticket.status"},
		{Entity: "Tickets", Field: "status", Value: 5, Setting: "ticket.resolvedstatus"},
		{Entity: "Tickets", Field: "ticketType", Value: 5, Setting: "ticket.type"},
		{Entity: "Tickets", Field: "source", Value: 8, Setting: "ticket.source"},
		{Entity: "Tickets", Field: "queueID", Value: 8, Setting: "ticket.queue"},
		{Entity: "TicketNotes", Field: "noteType", Value: 1, Setting: "note.type"},
		{Entity: "TicketNotes", Field: "publish", Value: 2, Setting: "note.publish"},
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing check %+v", want)
		}
	}
	for _, c := range got {
		if c.Field == "issueType" || c.Field == "subIssueType" || c.Field == "ticketCategory" {
			t.Errorf("unset optional field should not be checked: %+v", c)
		}
	}
}

func TestTracingExporter(t *testing.T) {
	if got := load(t, "--config", writeFile(t, "c.yaml", minimalYAML)).TracingExporter; got != "none" {
		t.Errorf("default TracingExporter = %q, want none", got)
	}

	for _, v := range []string{"none", "otlp", "console"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("AM_AUTOTASK_TRACING_EXPORTER", v)
			if got := load(t, "--config", writeFile(t, "c.yaml", minimalYAML)).TracingExporter; got != v {
				t.Errorf("TracingExporter = %q, want %q", got, v)
			}
		})
	}

	_, err := loadArgs([]string{"--config", writeFile(t, "c.yaml", minimalYAML), "--tracing.exporter", "jaeger"})
	if err == nil || !strings.Contains(err.Error(), "tracing.exporter") {
		t.Errorf("err = %v, want one naming tracing.exporter", err)
	}
}

func TestAutotaskFromFlags(t *testing.T) {
	loadAutotask := func(args ...string) (config.Autotask, error) {
		f := pflag.NewFlagSet("lookup", pflag.ContinueOnError)
		config.AddAutotaskFlags(f)
		if err := f.Parse(args); err != nil {
			return config.Autotask{}, err
		}
		return config.AutotaskFromFlags(f)
	}
	want := config.Autotask{Username: "api@example.com", Secret: "s3cret", IntegrationCode: "CODE", ZoneURL: autotask.DefaultZoneInformationURL}

	tests := []struct {
		name    string
		env     map[string]string
		args    func(t *testing.T) []string
		want    config.Autotask
		wantErr []string
	}{
		{
			name: "receiver configuration file, ignoring other settings",
			args: func(t *testing.T) []string {
				return []string{"--config", writeFile(t, "c.yaml", minimalYAML+"  queue: 8\n")}
			},
			want: want,
		},
		{
			name: "only Autotask settings are required",
			args: func(t *testing.T) []string {
				return []string{"--config", writeFile(t, "c.yaml", "autotask:\n  username: api@example.com\n  secret: s3cret\n  integrationcode: CODE\n")}
			},
			want: want,
		},
		{
			name: "environment variables and secret files",
			env:  map[string]string{"AM_AUTOTASK_AUTOTASK_USERNAME": "api@example.com", "AM_AUTOTASK_AUTOTASK_INTEGRATIONCODE": "CODE"},
			args: func(t *testing.T) []string {
				return []string{"--autotask.secretfile", writeFile(t, "secret", "s3cret\n"), "--autotask.zoneurl", "http://zone"}
			},
			want: config.Autotask{Username: "api@example.com", Secret: "s3cret", IntegrationCode: "CODE", ZoneURL: "http://zone"},
		},
		{
			name:    "missing settings are all reported",
			args:    func(*testing.T) []string { return nil },
			wantErr: []string{"autotask.username", "autotask.secret", "autotask.integrationcode"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := loadAutotask(tt.args(t)...)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatal("expected an error")
				}
				for _, key := range tt.wantErr {
					if !strings.Contains(err.Error(), key) {
						t.Errorf("error should mention %s: %v", key, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
