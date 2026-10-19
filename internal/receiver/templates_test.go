package receiver_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

func templatesFile(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "templates.tmpl")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustTemplates(t *testing.T, src string, loc *time.Location) *receiver.Templates {
	t.Helper()
	tmpl, err := receiver.ParseTemplates(templatesFile(t, src), loc)
	if err != nil {
		t.Fatalf("ParseTemplates: %v", err)
	}
	return tmpl
}

func TestParseTemplatesRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"syntax error", `{{define "ticket.title"}}{{.Receiver}{{end}}`, "parsing templates"},
		{"no known block", `{{define "ticket_title"}}x{{end}}`, "defines none of ticket.title"},
		{"unknown field", `{{define "ticket.title"}}{{.Company}}{{end}}`, "executing templates"},
		{"note field in ticket", `{{define "ticket.description"}}{{.TicketNumber}}{{end}}`, "executing templates"},
		{"unknown function", `{{define "note.title"}}{{sprig .Receiver}}{{end}}`, "parsing templates"},
		{"bad labels exclusion", `{{define "ticket.title"}}{{labels .GroupLabels 1}}{{end}}`, "labels cannot exclude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := receiver.ParseTemplates(templatesFile(t, tt.src), time.UTC)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseTemplates error = %v, want one containing %q", err, tt.want)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		if _, err := receiver.ParseTemplates(filepath.Join(t.TempDir(), "missing.tmpl"), time.UTC); err == nil {
			t.Error("ParseTemplates succeeded for a missing file")
		}
	})
}

func TestTemplatesFileOverridesOnlyTheBlocksItDefines(t *testing.T) {
	at := newFakeAutotask()
	opts := testOptions()
	opts.Templates = mustTemplates(t, `{{define "ticket.title"}}{{upper .GroupLabels.tenant_id}}: {{.Severity}} {{len .Alerts}} down{{end}}`, time.UTC)
	r := newReceiver(t, at, opts)

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	ticket := onlyTicket(t, at)
	if want := "FOO: critical 1 down"; ticket.Title != want {
		t.Errorf("Title = %q, want %q", ticket.Title, want)
	}
	if !strings.HasPrefix(ticket.Description, "Host is down\n\nGroup labels:") {
		t.Errorf("Description should keep the built-in template:\n%s", ticket.Description)
	}
}

func TestRenderedOutputIsTidied(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantTitle string
	}{
		{
			name:      "title becomes one line",
			src:       "{{define \"ticket.title\"}}\n  {{range .Alerts}}{{.Name}}\n{{end}}  on   {{.GroupLabels.tenant_id}}\n{{end}}",
			wantTitle: "HostDown HostDown on foo",
		},
		{
			name:      "empty title uses the built-in template",
			src:       `{{define "ticket.title"}} {{.GroupLabels.missing}} {{end}}`,
			wantTitle: `HostDown {tenant_id="foo"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			opts := testOptions()
			opts.Templates = mustTemplates(t, tt.src+"{{define \"ticket.description\"}}body\n\n  \n{{end}}", time.UTC)
			r := newReceiver(t, at, opts)

			handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")))

			ticket := onlyTicket(t, at)
			if ticket.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", ticket.Title, tt.wantTitle)
			}
			if ticket.Description != "body" {
				t.Errorf("Description = %q, want trailing whitespace trimmed", ticket.Description)
			}
		})
	}
}

func TestFailingTemplateFallsBackToBuiltIn(t *testing.T) {
	// passes against the sample notification, whose tenant is "example",
	// but fails for the "foo" tenant
	src := `{{define "ticket.title"}}{{if eq .GroupLabels.tenant_id "foo"}}{{index .Alerts 5}}{{end}}x{{end}}` +
		`{{define "note.description"}}{{if eq .GroupLabels.tenant_id "foo"}}{{index .Fired 5}}{{end}}x{{end}}`

	at := newFakeAutotask()
	obs := &recordingObserver{}
	opts := testOptions()
	opts.Observer = obs
	opts.Templates = mustTemplates(t, src, time.UTC)
	r := newReceiver(t, at, opts)

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")))

	ticket := onlyTicket(t, at)
	if want := `HostDown {tenant_id="foo"}`; ticket.Title != want {
		t.Errorf("Title = %q, want built-in %q", ticket.Title, want)
	}
	notes := at.notesFor(ticket.ID)
	if len(notes) != 1 || !strings.HasPrefix(notes[0].Description, "Firing (1):\n- HostDown") {
		t.Errorf("note should use the built-in description: %+v", notes)
	}
	if want := []string{"ticket.title", "note.description"}; !slices.Equal(obs.templates, want) {
		t.Errorf("failed templates reported = %v, want %v", obs.templates, want)
	}
}

func TestTemplatesCannotRemoveSystemNotices(t *testing.T) {
	at := newFakeAutotask()
	opts := testOptions()
	opts.Templates = mustTemplates(t, `{{define "ticket.description"}}only this{{end}}`, time.UTC)
	r := newReceiver(t, at, opts)

	n := notification(hostAlert("aaaa", "firing", "web1"))
	n.GroupLabels["tenant_id"] = "both"
	handle(t, r, n)

	desc := onlyTicket(t, at).Description
	if !strings.HasPrefix(desc, "NOTE: this alert group matched the company selectors of more than one company") || !strings.HasSuffix(desc, "\n\nonly this") {
		t.Errorf("Description should be the notice then the template output:\n%s", desc)
	}
}

func TestTemplateFunctions(t *testing.T) {
	perth, err := time.LoadLocation("Australia/Perth")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{"time in zone", `{{(index .Alerts 0).StartsAt | time}}`, "2026-10-17 11:50:00 AWST"},
		{"time with layout", `{{time (index .Alerts 0).StartsAt "15:04"}}`, "11:50"},
		{"labels", `{{labels .CommonLabels}}`, `{alertname="HostDown", severity="critical", tenant_id="foo"}`},
		{"labels excluding names and a map", `{{labels (index .Alerts 0).Labels .GroupLabels "severity"}}`, `{instance="web1"}`},
		{"default when empty", `{{default "none" .CommonLabels.team}}`, "none"},
		{"default when set", `{{default "none" .CommonLabels.severity}}`, "critical"},
		{"upper and lower", `{{upper "a"}}{{lower "B"}}`, "Ab"},
		{"trim", `[{{trim "  x  "}}]`, "[x]"},
		{"truncate", `{{truncate 16 "a long summary of the problem"}}`, "a l… (truncated)"},
		{"truncate shorter than marker", `{{truncate 3 "abcdef"}}`, "abc"},
		{"truncate short string", `{{truncate 16 "short"}}`, "short"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			opts := testOptions()
			opts.Templates = mustTemplates(t, `{{define "ticket.title"}}`+tt.body+`{{end}}`, perth)
			r := newReceiver(t, at, opts)

			handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

			if got := onlyTicket(t, at).Title; got != tt.want {
				t.Errorf("Title = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPreviewRendersEveryTemplate(t *testing.T) {
	tmpl := mustTemplates(t, `{{define "note.title"}}{{.TicketNumber}}: {{len .Fired}}/{{len .Resolved}}{{end}}`, time.UTC)
	n, err := receiver.SampleNotification()
	if err != nil {
		t.Fatal(err)
	}

	got, err := tmpl.Preview(n)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	want := map[string]string{
		"ticket.title":       `HostDown {tenant_id="example"}`,
		"note.title":         "T20261019.0001: 2/1",
		"ticket.description": "Host is not responding\n",
		"note.description":   "Firing (2):\n",
	}
	for name, prefix := range want {
		if !strings.HasPrefix(got[name], prefix) {
			t.Errorf("%s = %q, want it to start with %q", name, got[name], prefix)
		}
	}
}
