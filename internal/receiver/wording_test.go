package receiver_test

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

func TestWordingIsCutToAutotaskLimits(t *testing.T) {
	builtin, err := receiver.ParseTemplates("", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 40000)
	big := notification(manyAlerts(400, "firing")...)
	big.GroupLabels["instance_group"] = long
	ticket := receiver.Ticket{Number: long}

	tests := []struct {
		name string
		w    receiver.Wording
		// limits of the title and description fields written to
		title, description int
		// titleCut is set when the title is overlong too, rather than fixed
		// wording
		titleCut bool
	}{
		{"ticket", builtin.Ticket(receiver.TicketInput{Notification: big, Previous: &ticket}), 255, 8000, true},
		{"change note", builtin.ChangeNote(receiver.NoteInput{Notification: big, Fired: big.Alerts, TicketNumber: long}), 250, 32000, false},
		{"problem ticket", builtin.ProblemTicket(receiver.ProblemInput{Notification: big, TicketTitle: long, Linked: []string{long}}), 255, 8000, true},
		{"duplicate note", builtin.DuplicateNote(ticket), 250, 32000, false},
		{"linked note", builtin.LinkedNote(ticket), 250, 32000, true},
		{"recurrence failed note", builtin.RecurrenceFailedNote(receiver.RecurrenceFailure{Previous: ticket, Err: errors.New(long)}), 250, 32000, false},
		{"moved note", builtin.MovedNote(receiver.MovedInput{Notification: big, Moved: big.Alerts, Current: ticket}), 250, 32000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for field, got := range map[string]struct {
				s     string
				limit int
				cut   bool
			}{"title": {tt.w.Title, tt.title, tt.titleCut}, "description": {tt.w.Description, tt.description, true}} {
				if n := utf8.RuneCountInString(got.s); n > got.limit {
					t.Errorf("%s is %d characters, limit %d", field, n, got.limit)
				}
				if got.cut && !strings.HasSuffix(got.s, "… (truncated)") {
					t.Errorf("%s should end with a visible marker, ends %q", field, got.s[max(len(got.s)-40, 0):])
				}
			}
		})
	}
}

func TestPreviewFallsBackLikeTheReceiver(t *testing.T) {
	tmpl := mustTemplates(t, `{{define "ticket.title"}} {{.GroupLabels.missing}} {{end}}`, time.UTC)
	n, err := receiver.SampleNotification()
	if err != nil {
		t.Fatal(err)
	}

	got, err := tmpl.Preview(n, "", nil)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if want := `Host is not responding {tenant_id="example"}`; got[receiver.TemplateTicketTitle] != want {
		t.Errorf("an empty title should fall back to the built-in one: got %q, want %q", got[receiver.TemplateTicketTitle], want)
	}
}

func TestWordingReportsFallbacks(t *testing.T) {
	tmpl := mustTemplates(t, `{{define "ticket.title"}}{{if .GroupLabels.fail}}{{index .Alerts 5}}{{end}}{{.GroupLabels.missing}}{{end}}`, time.UTC)
	n := notification(hostAlert("aaaa", "firing", "web1"))

	t.Run("empty title", func(t *testing.T) {
		w := tmpl.Ticket(receiver.TicketInput{Notification: n})
		if len(w.Fallbacks) != 1 || w.Fallbacks[0].Template != receiver.TemplateTicketTitle || w.Fallbacks[0].Err != nil {
			t.Errorf("Fallbacks = %+v, want the empty ticket title", w.Fallbacks)
		}
	})
	t.Run("failing template", func(t *testing.T) {
		n.GroupLabels["fail"] = "yes"
		w := tmpl.Ticket(receiver.TicketInput{Notification: n})
		if len(w.Fallbacks) != 1 || w.Fallbacks[0].Template != receiver.TemplateTicketTitle || w.Fallbacks[0].Err == nil {
			t.Errorf("Fallbacks = %+v, want the failed ticket title", w.Fallbacks)
		}
		if w.Title == "" {
			t.Error("a failing title should fall back to the built-in one")
		}
	})
}
