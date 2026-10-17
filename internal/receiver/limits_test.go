package receiver_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

func manyAlerts(n int, status string) []receiver.Alert {
	alerts := make([]receiver.Alert, 0, n)
	for i := range n {
		alerts = append(alerts, hostAlert(fmt.Sprintf("%016x", i), status, fmt.Sprintf("web%d", i)))
	}
	return alerts
}

func TestOverlongTicketContentIsTruncatedVisibly(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())

	n := notification(manyAlerts(200, "firing")...)
	n.GroupLabels["instance_group"] = strings.Repeat("x", 300)
	handle(t, r, n)

	ticket := onlyTicket(t, at)
	if got := utf8.RuneCountInString(ticket.Title); got > 255 {
		t.Errorf("Title is %d characters, limit 255", got)
	}
	if got := utf8.RuneCountInString(ticket.Description); got > 8000 {
		t.Errorf("Description is %d characters, limit 8000", got)
	}
	if !strings.HasSuffix(ticket.Description, "… (truncated)") {
		t.Errorf("truncated description should end with a visible marker, ends %q",
			ticket.Description[len(ticket.Description)-40:])
	}
}

func TestOverlongNoteIsTruncatedVisibly(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	alerts := manyAlerts(400, "firing")
	for i := range alerts {
		alerts[i].Annotations["summary"] = strings.Repeat("long summary ", 10)
	}
	handle(t, r, notification(append(alerts, hostAlert("aaaa", "firing", "web1"))...))

	notes := at.notesFor(onlyTicket(t, at).ID)
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %d", len(notes))
	}
	if got := utf8.RuneCountInString(notes[0].Description); got > 32000 {
		t.Errorf("note description is %d characters, limit 32000", got)
	}
	if !strings.HasSuffix(notes[0].Description, "… (truncated)") {
		t.Error("truncated note should end with a visible marker")
	}
}

func TestGroupTooLargeToRecordStillResolves(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	// 600 fingerprints do not fit in the 8000 character recorded alerts UDF
	big := append(manyAlerts(600, "firing"), hostAlert("aaaa", "firing", "web1"))
	handle(t, r, notification(big...))

	ticket := onlyTicket(t, at)
	notes := at.notesFor(ticket.ID)
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %d", len(notes))
	}
	if !strings.Contains(notes[0].Description, "too many alerts") {
		t.Errorf("note should say individual alerts are no longer tracked:\n%.300s", notes[0].Description)
	}
	if got := utf8.RuneCountInString(ticket.RecordedAlerts); got > 8000 {
		t.Errorf("RecordedAlerts is %d characters, limit 8000", got)
	}

	// further changes cannot be told apart, so are not noted...
	handle(t, r, notification(append(big, hostAlert("bbbb", "firing", "web2"))...))
	if got := len(at.notesFor(ticket.ID)); got != 1 {
		t.Errorf("expected no further notes while too large, got %d notes", got)
	}

	// ...but the whole group resolving still completes the ticket
	handle(t, r, notification(manyAlerts(3, "resolved")...))
	if got := onlyTicket(t, at).Status; got != completeStatus {
		t.Errorf("Status = %d, want %d", got, completeStatus)
	}
}

func TestGroupTooLargeToRecordFromTheStart(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())

	big := manyAlerts(600, "firing")
	handle(t, r, notification(big...))
	handle(t, r, notification(append(big, hostAlert("bbbb", "firing", "web2"))...))

	ticket := onlyTicket(t, at)
	if got := utf8.RuneCountInString(ticket.RecordedAlerts); got > 8000 {
		t.Errorf("RecordedAlerts is %d characters, limit 8000", got)
	}
	if got := len(at.notesFor(ticket.ID)); got != 0 {
		t.Errorf("expected no notes while too large, got %d", got)
	}
}
