package receiver_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

func TestWholeGroupResolvingAddsNoteAndCompletesTicket(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")))

	handle(t, r, notification(hostAlert("aaaa", "resolved", "web1"), hostAlert("bbbb", "resolved", "web2")))

	ticket := onlyTicket(t, at)
	if ticket.Status != completeStatus {
		t.Errorf("Status = %d, want %d", ticket.Status, completeStatus)
	}
	if ticket.RecordedAlerts != "" {
		t.Errorf("RecordedAlerts = %q, want empty", ticket.RecordedAlerts)
	}
	notes := at.notesFor(ticket.ID)
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %d", len(notes))
	}
	if !strings.Contains(notes[0].Title, "resolved") || !strings.Contains(notes[0].Description, `instance="web2"`) {
		t.Errorf("note should record the group resolving: %q\n%s", notes[0].Title, notes[0].Description)
	}
}

func TestWholeGroupResolvingLeavesStatusWhenDisabled(t *testing.T) {
	at := newFakeAutotask()
	opts := testOptions()
	opts.ResolvedStatus = 0
	r := newReceiver(t, at, opts)
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	handle(t, r, notification(hostAlert("aaaa", "resolved", "web1")))

	ticket := onlyTicket(t, at)
	if ticket.Status != 1 {
		t.Errorf("Status = %d, want unchanged 1", ticket.Status)
	}
	if len(at.notesFor(ticket.ID)) != 1 {
		t.Error("expected the resolution to be noted")
	}
}

func TestFailedCompletionIsRetried(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
	resolved := notification(hostAlert("aaaa", "resolved", "web1"))

	at.failNext("UpdateTicket", errors.New("timeout"))
	if err := r.Handle(context.Background(), resolved); err == nil {
		t.Fatal("expected Handle to report the failure so Alertmanager retries")
	}
	handle(t, r, resolved)

	if got := onlyTicket(t, at).Status; got != completeStatus {
		t.Errorf("Status = %d, want %d after retry", got, completeStatus)
	}
}

func TestResolvedNotificationWithoutCurrentTicketCreatesNothing(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())

	handle(t, r, notification(hostAlert("aaaa", "resolved", "web1")))

	if tickets := at.allTickets(); len(tickets) != 0 {
		t.Errorf("expected no tickets, got %d", len(tickets))
	}
}

func TestGroupFiringAgainAfterCompletionGetsNewTicketReferencingPrevious(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
	handle(t, r, notification(hostAlert("aaaa", "resolved", "web1")))
	first := onlyTicket(t, at)

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	tickets := at.allTickets()
	if len(tickets) != 2 {
		t.Fatalf("expected a second ticket, got %d tickets", len(tickets))
	}
	if second := tickets[1]; !strings.Contains(second.Description, first.Number) {
		t.Errorf("new ticket should reference %s:\n%s", first.Number, second.Description)
	}
}

func TestTicketClosedWhileFiringIsNeverReopened(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	n := notification(hostAlert("aaaa", "firing", "web1"))
	handle(t, r, n)
	first := onlyTicket(t, at)
	at.closeTicket(first.ID)

	// e.g. the next repeat_interval re-send
	handle(t, r, n)

	tickets := at.allTickets()
	if len(tickets) != 2 {
		t.Fatalf("expected a new ticket, got %d tickets", len(tickets))
	}
	if tickets[0].Status != completeStatus {
		t.Errorf("closed ticket was reopened: status %d", tickets[0].Status)
	}
	if !strings.Contains(tickets[1].Description, first.Number) {
		t.Errorf("new ticket should reference %s:\n%s", first.Number, tickets[1].Description)
	}
}

func TestDuplicateOpenTicketsUseOldestAndWarnOnTheOthersOnce(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	n := notification(hostAlert("aaaa", "firing", "web1"))
	// two tickets for one group, e.g. from two replicas racing
	for range 2 {
		if _, err := at.CreateTicket(context.Background(), receiver.NewTicket{
			ExternalID: receiver.ExternalID(n.GroupKey), Status: 1, RecordedAlerts: "aaaa",
		}); err != nil {
			t.Fatal(err)
		}
	}
	tickets := at.allTickets()
	oldest, duplicate := tickets[0], tickets[1]

	n2 := notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2"))
	handle(t, r, n2)
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2"), hostAlert("cccc", "firing", "web3")))

	if notes := at.notesFor(oldest.ID); len(notes) != 2 {
		t.Errorf("oldest ticket should receive both changes, got %d notes", len(notes))
	}
	notes := at.notesFor(duplicate.ID)
	if len(notes) != 1 {
		t.Fatalf("duplicate should be warned exactly once, got %d notes", len(notes))
	}
	if !strings.Contains(notes[0].Description, oldest.Number) {
		t.Errorf("warning should name the ticket in use, %s:\n%s", oldest.Number, notes[0].Description)
	}
}
