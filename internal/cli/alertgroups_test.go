package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// alertGroupsJSON is /api/v2/alerts/groups output with two groups for the
// autotask receiver, the second of them entirely silenced, and one for
// another receiver.
const alertGroupsJSON = `[
  {
    "labels": {"alertname": "HostDown", "tenant_id": "acme"},
    "receiver": {"name": "autotask"},
    "alerts": [
      {
        "labels": {"alertname": "HostDown", "instance": "web1:9100", "severity": "critical", "tenant_id": "acme"},
        "annotations": {"summary": "web1 is down", "runbook_url": "https://runbooks.example.com/HostDown"},
        "startsAt": "2026-11-24T01:00:00Z",
        "endsAt": "2026-11-24T02:04:00Z",
        "updatedAt": "2026-11-24T02:00:00Z",
        "generatorURL": "https://prometheus.example.com/graph?g0.expr=up",
        "fingerprint": "aa",
        "receivers": [{"name": "autotask"}],
        "status": {"state": "active", "silencedBy": [], "inhibitedBy": [], "mutedBy": []}
      },
      {
        "labels": {"alertname": "HostDown", "instance": "web2:9100", "severity": "critical", "tenant_id": "acme"},
        "annotations": {"summary": "web2 is down", "runbook_url": "https://runbooks.example.com/HostDown"},
        "startsAt": "2026-11-24T01:05:00Z",
        "endsAt": "2026-11-24T02:04:00Z",
        "fingerprint": "bb",
        "status": {"state": "unprocessed"}
      },
      {
        "labels": {"alertname": "HostDown", "instance": "web3:9100", "severity": "warning", "tenant_id": "acme"},
        "annotations": {"summary": "web3 is down"},
        "startsAt": "2026-11-24T01:10:00Z",
        "endsAt": "2026-11-24T02:04:00Z",
        "fingerprint": "cc",
        "status": {"state": "suppressed", "silencedBy": ["1234"]}
      }
    ]
  },
  {
    "labels": {"alertname": "DiskFull", "tenant_id": "acme"},
    "receiver": {"name": "autotask"},
    "alerts": [
      {
        "labels": {"alertname": "DiskFull", "tenant_id": "acme"},
        "startsAt": "2026-11-24T01:00:00Z",
        "fingerprint": "dd",
        "status": {"state": "suppressed", "inhibitedBy": ["ee"]}
      }
    ]
  },
  {
    "labels": {"alertname": "HostDown"},
    "receiver": {"name": "slack"},
    "alerts": [
      {
        "labels": {"alertname": "HostDown", "instance": "web1:9100"},
        "startsAt": "2026-11-24T01:00:00Z",
        "fingerprint": "ff",
        "status": {"state": "active"}
      }
    ]
  }
]`

func TestParseAlertGroups(t *testing.T) {
	groups, err := parseAlertGroups([]byte(alertGroupsJSON), "groups.json", "https://am.example.net")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(groups))
	}

	tests := []struct {
		name        string
		group       previewGroup
		heading     string
		skipped     bool
		fingerprint []string
	}{
		{
			name:        "suppressed alerts are left out",
			group:       groups[0],
			heading:     `alert group 1 of 3: receiver "autotask" {alertname="HostDown", tenant_id="acme"}`,
			fingerprint: []string{"aa", "bb"},
		},
		{
			name:    "a group with every alert suppressed is skipped",
			group:   groups[1],
			heading: `alert group 2 of 3: receiver "autotask" {alertname="DiskFull", tenant_id="acme"}`,
			skipped: true,
		},
		{
			name:        "groups for other receivers are kept",
			group:       groups[2],
			heading:     `alert group 3 of 3: receiver "slack" {alertname="HostDown"}`,
			fingerprint: []string{"ff"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.group.heading != tt.heading {
				t.Errorf("heading = %q, want %q", tt.group.heading, tt.heading)
			}
			if tt.group.skipped != tt.skipped {
				t.Errorf("skipped = %v, want %v", tt.group.skipped, tt.skipped)
			}
			var got []string
			for _, a := range tt.group.notification.Alerts {
				got = append(got, a.Fingerprint)
			}
			if !reflect.DeepEqual(got, tt.fingerprint) {
				t.Errorf("alerts = %v, want %v", got, tt.fingerprint)
			}
		})
	}
}

func TestAlertGroupNotification(t *testing.T) {
	groups, err := parseAlertGroups([]byte(alertGroupsJSON), "groups.json", "https://am.example.net")
	if err != nil {
		t.Fatal(err)
	}
	n := groups[0].notification

	if n.Status != "firing" || n.Receiver != "autotask" || n.ExternalURL != "https://am.example.net" {
		t.Errorf("status, receiver, external URL = %q, %q, %q", n.Status, n.Receiver, n.ExternalURL)
	}
	if want := map[string]string{"alertname": "HostDown", "tenant_id": "acme"}; !reflect.DeepEqual(n.GroupLabels, want) {
		t.Errorf("group labels = %v, want %v", n.GroupLabels, want)
	}
	// the suppressed warning is not sent, so every alert sent is critical
	if want := map[string]string{"alertname": "HostDown", "severity": "critical", "tenant_id": "acme"}; !reflect.DeepEqual(n.CommonLabels, want) {
		t.Errorf("common labels = %v, want %v", n.CommonLabels, want)
	}
	if want := map[string]string{"runbook_url": "https://runbooks.example.com/HostDown"}; !reflect.DeepEqual(n.CommonAnnotations, want) {
		t.Errorf("common annotations = %v, want %v", n.CommonAnnotations, want)
	}
	a := n.Alerts[0]
	if a.Status != "firing" || !a.EndsAt.IsZero() {
		t.Errorf("alert status %q, ends at %v; want firing with a zero end", a.Status, a.EndsAt)
	}
	if !a.StartsAt.Equal(time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)) || a.GeneratorURL == "" || a.Annotations["summary"] != "web1 is down" {
		t.Errorf("alert not copied: %+v", a)
	}
}

func TestParseInputErrors(t *testing.T) {
	tests := []struct {
		name    string
		parse   func([]byte) error
		input   string
		want    string
		wantErr error
	}{
		{
			name:  "webhook payload given as alert groups",
			parse: func(b []byte) error { _, err := parseAlertGroups(b, "x.json", ""); return err },
			input: ` {"version":"4","alerts":[]}`,
			want:  "looks like a webhook payload, use --notification",
		},
		{
			name:  "alert groups given as a webhook payload",
			parse: func(b []byte) error { _, err := parseNotification(b, "x.json"); return err },
			input: "\n[]",
			want:  "looks like /api/v2/alerts/groups output, use --alert-groups",
		},
		{
			name:    "no alert groups",
			parse:   func(b []byte) error { _, err := parseAlertGroups(b, "x.json", ""); return err },
			input:   `[]`,
			wantErr: errNoAlertGroups,
		},
		{
			name:    "every alert group skipped",
			parse:   func(b []byte) error { _, err := parseAlertGroups(b, "x.json", ""); return err },
			input:   `[{"labels":{"a":"b"},"receiver":{"name":"r"},"alerts":[{"status":{"state":"suppressed"}}]}]`,
			wantErr: errNoAlertGroups,
		},
		{
			name:  "malformed alert groups",
			parse: func(b []byte) error { _, err := parseAlertGroups(b, "x.json", ""); return err },
			input: `[{"labels":`,
			want:  "parsing alert groups x.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.parse([]byte(tt.input))
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should contain %q", err, tt.want)
			}
		})
	}
}

func TestTemplatesPreviewsAlertGroups(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "t.tmpl")
	if err := os.WriteFile(tmplPath, []byte(`{{define "ticket.title"}}{{.Receiver}} {{len .Alerts}} {{.ExternalURL}}{{end}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	groupsPath := filepath.Join(dir, "groups.json")
	if err := os.WriteFile(groupsPath, []byte(alertGroupsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		stdin string
		args  []string
		want  []string
	}{
		{
			name: "from a file with the sample's external URL",
			args: []string{"--alert-groups", groupsPath},
			want: []string{
				"##### alert group 1 of 3: receiver \"autotask\" {alertname=\"HostDown\", tenant_id=\"acme\"}\n\n=== ticket.title\nautotask 2 https://alertmanager.example.com\n\n",
				"##### alert group 2 of 3: receiver \"autotask\" {alertname=\"DiskFull\", tenant_id=\"acme\"}\n\nskipped: every alert is silenced or inhibited\n\n",
				"##### alert group 3 of 3: receiver \"slack\" {alertname=\"HostDown\"}\n\n=== ticket.title\nslack 1 https://alertmanager.example.com\n\n",
			},
		},
		{
			name:  "from standard input with an external URL",
			stdin: alertGroupsJSON,
			args:  []string{"--alert-groups", "-", "--external-url", "https://am.example.net"},
			want:  []string{"=== ticket.title\nautotask 2 https://am.example.net\n\n"},
		},
		{
			name:  "a webhook payload from standard input",
			stdin: `{"version":"4","status":"firing","receiver":"ops","externalURL":"https://am.example.org","alerts":[]}`,
			args:  []string{"--notification", "-"},
			want:  []string{"=== ticket.title\nops 0 https://am.example.org\n\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runTemplatesWithInput(t, tt.stdin, append([]string{"--templates", tmplPath}, tt.args...)...)
			if err != nil {
				t.Fatalf("templates: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("preview missing %q:\n%s", want, got)
				}
			}
			if n := strings.Count(got, "System notices"); n != 1 {
				t.Errorf("system notices line appears %d times, want once:\n%s", n, got)
			}
		})
	}
}

func TestTemplatesAlertGroupsErrors(t *testing.T) {
	dir := t.TempDir()
	// passes against the sample, which has two firing alerts, but fails on
	// a group with one
	tmplPath := filepath.Join(dir, "t.tmpl")
	if err := os.WriteFile(tmplPath, []byte(`{{define "ticket.title"}}{{(index .Alerts 1).Name}}{{end}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	groupsPath := filepath.Join(dir, "groups.json")
	if err := os.WriteFile(groupsPath, []byte(alertGroupsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		args    []string
		wantErr string
		wantOut []string
	}{
		{
			name:    "a failing group does not stop the others",
			args:    []string{"--alert-groups", groupsPath},
			wantErr: `alert group 3 of 3: receiver "slack"`,
			wantOut: []string{
				"=== ticket.title\nHostDown\n\n",
				"##### alert group 3 of 3: receiver \"slack\" {alertname=\"HostDown\"}\n\nerror: alert group 3 of 3",
				"System notices",
			},
		},
		{
			name:    "external URL without alert groups",
			args:    []string{"--external-url", "https://am.example.net"},
			wantErr: "--external-url can only be used with --alert-groups",
		},
		{
			name:    "relative external URL",
			args:    []string{"--alert-groups", groupsPath, "--external-url", "am.example.net"},
			wantErr: "is not an absolute URL",
		},
		{
			name:    "notification and alert groups together",
			args:    []string{"--alert-groups", groupsPath, "--notification", groupsPath},
			wantErr: "none of the others can be",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runTemplatesWithInput(t, "", append([]string{"--templates", tmplPath}, tt.args...)...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(got, want) {
					t.Errorf("preview missing %q:\n%s", want, got)
				}
			}
		})
	}
}
