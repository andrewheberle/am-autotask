package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if err := previewTemplates(&b, tmpl, n); err != nil {
		t.Fatalf("previewTemplates: %v", err)
	}

	got := b.String()
	for _, want := range []string{"=== ticket.title\nops: acme\n\n", "=== ticket.description\n", "=== note.title\nAlert group: 0 firing, 0 resolved\n\n", "=== note.description\n", "System notices"} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing %q:\n%s", want, got)
		}
	}
}
