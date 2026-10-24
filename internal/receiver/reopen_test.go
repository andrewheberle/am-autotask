package receiver_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// Statuses for tickets left awaiting closure, reopened and moved by a
// technician.
const (
	pendingClosure = 22
	inProgress     = 8
	onHold         = 17
)

const reopenNotice = "has been reopened because its alert group fired again"

func reopenOptions() receiver.Options {
	opts := testOptions()
	opts.ResolvedStatus = pendingClosure
	opts.ReopenStatus = inProgress
	return opts
}

// setStatus simulates a technician changing a ticket's status.
func (f *fakeAutotask) setStatus(id int64, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ticket(id).Status = status
}

func TestTicketAwaitingClosureIsReopened(t *testing.T) {
	tests := []struct {
		name         string
		reopenStatus int
		wantStatus   int
		wantReopened int
	}{
		{"reopen enabled", inProgress, inProgress, 1},
		// pins the behaviour before reopening was added
		{"reopen disabled", 0, pendingClosure, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			obs := &recordingObserver{}
			sr := tracetest.NewSpanRecorder()
			opts := reopenOptions()
			opts.ReopenStatus = tt.reopenStatus
			opts.Observer = obs
			opts.TracerProvider = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
			r := newReceiver(t, at, opts)
			handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
			handle(t, r, notification(hostAlert("aaaa", "resolved", "web1")))
			if got := onlyTicket(t, at).Status; got != pendingClosure {
				t.Fatalf("Status = %d, want %d once the group resolved", got, pendingClosure)
			}

			handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

			ticket := onlyTicket(t, at)
			if ticket.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", ticket.Status, tt.wantStatus)
			}
			if ticket.RecordedAlerts != "aaaa" {
				t.Errorf("RecordedAlerts = %q, want %q", ticket.RecordedAlerts, "aaaa")
			}
			notes := at.notesFor(ticket.ID)
			if len(notes) != 2 {
				t.Fatalf("expected two notes, got %d", len(notes))
			}
			if got := strings.Contains(notes[1].Description, reopenNotice); got != (tt.wantReopened > 0) {
				t.Errorf("reopen notice in note = %v, want %v:\n%s", got, tt.wantReopened > 0, notes[1].Description)
			}
			if obs.reopened != tt.wantReopened {
				t.Errorf("reopened = %d, want %d", obs.reopened, tt.wantReopened)
			}
			spans := handleSpans(sr)
			_, ok := attrs(spans[len(spans)-1])["am_autotask.reopened"]
			if ok != (tt.wantReopened > 0) {
				t.Errorf("am_autotask.reopened span attribute present = %v, want %v", ok, tt.wantReopened > 0)
			}
		})
	}
}

func TestTicketNotAwaitingClosureIsNotReopened(t *testing.T) {
	firing := notification(hostAlert("aaaa", "firing", "web1"))
	resolved := notification(hostAlert("aaaa", "resolved", "web1"))
	tests := []struct {
		name string
		// setup takes the ticket to the state under test
		setup      func(t *testing.T, at *fakeAutotask, r *receiver.Receiver, id int64)
		next       receiver.Notification
		wantStatus int
		wantNotes  int
	}{
		{
			name: "moved by a technician to another status",
			setup: func(t *testing.T, at *fakeAutotask, r *receiver.Receiver, id int64) {
				handle(t, r, resolved)
				at.setStatus(id, onHold)
			},
			next:       firing,
			wantStatus: onHold,
			wantNotes:  2,
		},
		{
			// alerts are still recorded, so the status is a person's decision
			name: "set to the resolved status by a technician",
			setup: func(t *testing.T, at *fakeAutotask, r *receiver.Receiver, id int64) {
				at.setStatus(id, pendingClosure)
			},
			next:       notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")),
			wantStatus: pendingClosure,
			wantNotes:  1,
		},
		{
			name: "resolved notification re-sent",
			setup: func(t *testing.T, at *fakeAutotask, r *receiver.Receiver, id int64) {
				handle(t, r, resolved)
			},
			next:       resolved,
			wantStatus: pendingClosure,
			wantNotes:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			obs := &recordingObserver{}
			opts := reopenOptions()
			opts.Observer = obs
			r := newReceiver(t, at, opts)
			handle(t, r, firing)
			tt.setup(t, at, r, onlyTicket(t, at).ID)

			handle(t, r, tt.next)

			ticket := onlyTicket(t, at)
			if ticket.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", ticket.Status, tt.wantStatus)
			}
			notes := at.notesFor(ticket.ID)
			if len(notes) != tt.wantNotes {
				t.Fatalf("expected %d notes, got %d", tt.wantNotes, len(notes))
			}
			for _, n := range notes {
				if strings.Contains(n.Description, reopenNotice) {
					t.Errorf("note should not say the ticket was reopened:\n%s", n.Description)
				}
			}
			if obs.reopened != 0 {
				t.Errorf("reopened = %d, want 0", obs.reopened)
			}
		})
	}
}

func TestCompletedTicketIsNotReopened(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, reopenOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
	handle(t, r, notification(hostAlert("aaaa", "resolved", "web1")))
	previous := onlyTicket(t, at)
	at.closeTicket(previous.ID)

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	if got := refreshed(t, at, previous).Status; got != completeStatus {
		t.Errorf("previous ticket Status = %d, want %d", got, completeStatus)
	}
	if got := len(at.allTickets()); got != 2 {
		t.Errorf("expected a new ticket, got %d tickets", got)
	}
}

func TestFailedReopenIsRetried(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, reopenOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
	handle(t, r, notification(hostAlert("aaaa", "resolved", "web1")))
	firing := notification(hostAlert("aaaa", "firing", "web1"))

	at.failNext("UpdateTicket", errors.New("timeout"))
	if err := r.Handle(context.Background(), firing); err == nil {
		t.Fatal("expected Handle to report the failure so Alertmanager retries")
	}
	handle(t, r, firing)

	ticket := onlyTicket(t, at)
	if ticket.Status != inProgress {
		t.Errorf("Status = %d, want %d after retry", ticket.Status, inProgress)
	}
	if ticket.RecordedAlerts != "aaaa" {
		t.Errorf("RecordedAlerts = %q, want %q", ticket.RecordedAlerts, "aaaa")
	}
}

func TestRevivedOrphanAwaitingClosureIsReopened(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, reopenOptions())
	before := notification(hostAlert("aaaa", "firing", "web1"))
	handle(t, r, before)
	orphan := ticketFor(t, at, before)
	handle(t, r, regrouped("perth", hostAlert("aaaa", "firing", "web1")))
	if got := refreshed(t, at, orphan).Status; got != pendingClosure {
		t.Fatalf("orphaned ticket Status = %d, want %d once emptied", got, pendingClosure)
	}

	// the change to Alertmanager's grouping is reverted
	handle(t, r, before)

	orphan = refreshed(t, at, orphan)
	if orphan.Status != inProgress {
		t.Errorf("Status = %d, want %d", orphan.Status, inProgress)
	}
	if orphan.RecordedAlerts != "aaaa" {
		t.Errorf("RecordedAlerts = %q, want %q", orphan.RecordedAlerts, "aaaa")
	}
}

func TestReopenStatusIsValidated(t *testing.T) {
	tests := []struct {
		name           string
		resolvedStatus int
		reopenStatus   int
		wantErr        bool
	}{
		{"disabled", completeStatus, 0, false},
		{"valid", pendingClosure, inProgress, false},
		{"resolved status disabled", 0, inProgress, true},
		{"resolved status Complete", completeStatus, inProgress, true},
		{"reopen status Complete", pendingClosure, completeStatus, true},
		{"reopen status same as resolved status", pendingClosure, pendingClosure, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := testOptions()
			opts.ResolvedStatus = tt.resolvedStatus
			opts.ReopenStatus = tt.reopenStatus
			_, err := receiver.New(newFakeAutotask(), fakeMatcher{}, receiver.NewMemoryLocker(), opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("New error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

func TestNoteTemplatesSeeReopened(t *testing.T) {
	at := newFakeAutotask()
	opts := reopenOptions()
	opts.Templates = mustTemplates(t, `{{define "note.title"}}{{if .Reopened}}Reopened{{else}}Changed{{end}}{{end}}`, time.UTC)
	r := newReceiver(t, at, opts)
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
	handle(t, r, notification(hostAlert("aaaa", "resolved", "web1")))

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	notes := at.notesFor(onlyTicket(t, at).ID)
	var got []string
	for _, n := range notes {
		got = append(got, n.Title)
	}
	if !slices.Equal(got, []string{"Changed", "Reopened"}) {
		t.Errorf("note titles = %v, want [Changed Reopened]", got)
	}
}
