package receiver_test

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewlyFiringAlertAddsNoteAndIsRecorded(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")))

	ticket := onlyTicket(t, at)
	notes := at.notesFor(ticket.ID)
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %d", len(notes))
	}
	note := notes[0]
	if note.Title != "1 firing, 0 resolved" {
		t.Errorf("note Title = %q, want %q", note.Title, "1 firing, 0 resolved")
	}
	if !strings.Contains(note.Description, `instance="web2"`) || strings.Contains(note.Description, `instance="web1"`) {
		t.Errorf("note should list only the newly firing alert:\n%s", note.Description)
	}
	if note.NoteType != 1 || note.Publish != 2 {
		t.Errorf("note type/publish = %d/%d, want 1/2", note.NoteType, note.Publish)
	}
	if ticket.RecordedAlerts != "aaaa,bbbb" {
		t.Errorf("RecordedAlerts = %q, want %q", ticket.RecordedAlerts, "aaaa,bbbb")
	}
}

func TestResolvedAlertAddsNoteAndIsNoLongerRecorded(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")))

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "resolved", "web2")))

	ticket := onlyTicket(t, at)
	notes := at.notesFor(ticket.ID)
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %d", len(notes))
	}
	if notes[0].Title != "0 firing, 1 resolved" {
		t.Errorf("note Title = %q, want %q", notes[0].Title, "0 firing, 1 resolved")
	}
	if !strings.Contains(notes[0].Description, `instance="web2"`) || !strings.Contains(notes[0].Description, "Ended:") {
		t.Errorf("note should list the resolved alert with its end time:\n%s", notes[0].Description)
	}
	if ticket.RecordedAlerts != "aaaa" {
		t.Errorf("RecordedAlerts = %q, want %q", ticket.RecordedAlerts, "aaaa")
	}
	if ticket.Status != 1 {
		t.Errorf("Status = %d, want unchanged 1 while the group is still firing", ticket.Status)
	}
}

func TestUnchangedNotificationAddsNothing(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	n := notification(hostAlert("aaaa", "firing", "web1"))
	handle(t, r, n)

	// e.g. a repeat_interval re-send
	handle(t, r, n)

	ticket := onlyTicket(t, at)
	if notes := at.notesFor(ticket.ID); len(notes) != 0 {
		t.Errorf("expected no notes, got %d", len(notes))
	}
}

func TestResolvedAlertThatWasNeverRecordedIsIgnored(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("zzzz", "resolved", "web9")))

	if notes := at.notesFor(onlyTicket(t, at).ID); len(notes) != 0 {
		t.Errorf("expected no notes, got %d", len(notes))
	}
}

func TestAlertMissingFromNotificationStaysRecorded(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")))

	// only an explicit resolved status removes an alert
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("cccc", "firing", "web3")))

	if got := onlyTicket(t, at).RecordedAlerts; got != "aaaa,bbbb,cccc" {
		t.Errorf("RecordedAlerts = %q, want %q", got, "aaaa,bbbb,cccc")
	}
}

func TestFailedRecordingIsRetriedWithoutLosingTheNote(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
	n := notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2"))

	at.failNext("UpdateTicket", errors.New("timeout"))
	if err := r.Handle(context.Background(), n); err == nil {
		t.Fatal("expected Handle to report the failure so Alertmanager retries")
	}
	handle(t, r, n)

	ticket := onlyTicket(t, at)
	if ticket.RecordedAlerts != "aaaa,bbbb" {
		t.Errorf("RecordedAlerts = %q, want %q", ticket.RecordedAlerts, "aaaa,bbbb")
	}
	if notes := at.notesFor(ticket.ID); len(notes) == 0 {
		t.Error("the note for bbbb was lost")
	}
}

func TestFailedNoteIsRetried(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
	n := notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2"))

	at.failNext("CreateNote", errors.New("timeout"))
	if err := r.Handle(context.Background(), n); err == nil {
		t.Fatal("expected Handle to report the failure so Alertmanager retries")
	}
	handle(t, r, n)

	if notes := at.notesFor(onlyTicket(t, at).ID); len(notes) != 1 {
		t.Errorf("expected exactly one note after the retry, got %d", len(notes))
	}
}
