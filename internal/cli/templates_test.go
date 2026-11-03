package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/config"
	"github.com/andrewheberle/am-autotask/internal/receiver"
)

func TestPreviewTemplates(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "t.tmpl")
	if err := os.WriteFile(tmplPath, []byte(`{{define "ticket.title"}}{{.Receiver}}: {{.GroupLabels.tenant_id}}{{end}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "webhook.json")
	if err := os.WriteFile(payload, []byte(`{"version":"4","status":"firing","receiver":"ops","groupLabels":{"tenant_id":"acme"},"alerts":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl, err := receiver.ParseTemplates(tmplPath, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	n, err := readNotification(payload)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := previewTemplates(&b, tmpl, n, config.Preview{}); err != nil {
		t.Fatalf("previewTemplates: %v", err)
	}

	got := b.String()
	for _, want := range []string{"=== ticket.title\nops: acme\n\n", "=== ticket.description\n", "=== note.title\nAlert group: 0 firing, 0 resolved\n\n", "=== note.description\n", "System notices"} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing %q:\n%s", want, got)
		}
	}
}

// runTemplates runs the templates command with args, returning what it
// writes to standard output.
func runTemplates(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("AM_AUTOTASK_CONFIG", "")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	runErr := execute(t.Context(), append([]string{"templates"}, args...), "v1.2.3")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("templates %v: %v", args, runErr)
	}
	return string(out)
}

func TestTemplatesPreviewUsesTheConfiguredSeverity(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "t.tmpl")
	if err := os.WriteFile(tmplPath, []byte(`{{define "ticket.title"}}severity {{.Severity}}{{end}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "webhook.json")
	if err := os.WriteFile(payload, []byte(`{"version":"4","status":"firing","receiver":"ops",
		"groupLabels":{"tenant_id":"acme"},"commonLabels":{"tenant_id":"acme","severity":"none"},
		"alerts":[
			{"status":"firing","labels":{"alertname":"A","level":"warning","severity":"none"},"fingerprint":"aa"},
			{"status":"firing","labels":{"alertname":"B","level":"critical","severity":"none"},"fingerprint":"bb"}
		]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name: "common label by default",
			want: "severity none",
		},
		{
			name:   "configured label",
			config: "ticket:\n  severitylabel: level\n",
			// the alerts' levels differ, so there is no common level, and
			// the title is tidied to one line
			want: "severity",
		},
		{
			name:   "highest firing severity with an order",
			config: "ticket:\n  severitylabel: level\n  severityorder: [critical, warning]\n",
			want:   "severity critical",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"--templates", tmplPath, "--notification", payload}
			if tt.config != "" {
				cfg := filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(cfg, []byte(tt.config), 0o644); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--config", cfg)
			}

			got := runTemplates(t, args...)
			if want := "=== ticket.title\n" + tt.want + "\n"; !strings.Contains(got, want) {
				t.Errorf("preview should contain %q:\n%s", want, got)
			}
		})
	}
}
