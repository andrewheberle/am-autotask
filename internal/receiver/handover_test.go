package receiver_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// regrouped returns a notification for the same alerts under another group
// key, as after a change to Alertmanager's routes or group_by.
func regrouped(site string, alerts ...receiver.Alert) receiver.Notification {
	n := notification(alerts...)
	n.GroupKey = `{}/{severity="critical"}:{alertname="HostDown", site="` + site + `", tenant_id="foo"}`
	n.GroupLabels["site"] = site
	return n
}

func ticketFor(t *testing.T, at *fakeAutotask, n receiver.Notification) fakeTicket {
	t.Helper()
	want := receiver.ExternalID(n.GroupKey)
	for _, tk := range at.allTickets() {
		if tk.ExternalID == want {
			return tk
		}
	}
	t.Fatalf("no ticket for group key %s", n.GroupKey)
	return fakeTicket{}
}

func refreshed(t *testing.T, at *fakeAutotask, tk fakeTicket) fakeTicket {
	t.Helper()
	for _, x := range at.allTickets() {
		if x.ID == tk.ID {
			return x
		}
	}
	t.Fatalf("ticket %d disappeared", tk.ID)
	return fakeTicket{}
}

func TestGroupKeyChangeMovesAlertsAndCompletesOrphanedTicket(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	before := notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2"))
	handle(t, r, before)
	orphan := ticketFor(t, at, before)

	after := regrouped("perth", hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2"))
	handle(t, r, after)

	successor := ticketFor(t, at, after)
	if !strings.Contains(successor.Description, orphan.Number) {
		t.Errorf("new ticket should say it continues %s:\n%s", orphan.Number, successor.Description)
	}
	orphan = refreshed(t, at, orphan)
	if orphan.Status != completeStatus {
		t.Errorf("orphaned ticket status = %d, want %d", orphan.Status, completeStatus)
	}
	if orphan.RecordedAlerts != "" {
		t.Errorf("orphaned ticket RecordedAlerts = %q, want empty", orphan.RecordedAlerts)
	}
	notes := at.notesFor(orphan.ID)
	if len(notes) != 1 {
		t.Fatalf("expected one note on the orphaned ticket, got %d", len(notes))
	}
	if !strings.Contains(notes[0].Description, successor.Number) || !strings.Contains(notes[0].Description, `instance="web2"`) {
		t.Errorf("note should list the moved alerts and name %s:\n%s", successor.Number, notes[0].Description)
	}
	if got := at.notesFor(successor.ID); len(got) != 0 {
		t.Errorf("new ticket should not get notes, got %d", len(got))
	}
}

func TestSplitGroupMovesAlertsInSteps(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	before := notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2"))
	handle(t, r, before)
	orphan := ticketFor(t, at, before)

	handle(t, r, regrouped("perth", hostAlert("aaaa", "firing", "web1")))

	orphan = refreshed(t, at, orphan)
	if orphan.Status == completeStatus {
		t.Fatal("orphaned ticket completed while it still had an alert recorded")
	}
	if orphan.RecordedAlerts != "bbbb" {
		t.Errorf("RecordedAlerts = %q, want %q", orphan.RecordedAlerts, "bbbb")
	}

	handle(t, r, regrouped("sydney", hostAlert("bbbb", "firing", "web2")))

	orphan = refreshed(t, at, orphan)
	if orphan.Status != completeStatus {
		t.Errorf("status = %d, want %d once every alert has moved", orphan.Status, completeStatus)
	}
	if got := len(at.notesFor(orphan.ID)); got != 2 {
		t.Errorf("expected a note per move, got %d", got)
	}
}

func TestAlertJoiningExistingTicketIsMovedOffOrphan(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	before := notification(hostAlert("aaaa", "firing", "web1"))
	handle(t, r, before)
	orphan := ticketFor(t, at, before)
	handle(t, r, regrouped("perth", hostAlert("cccc", "firing", "web3")))
	current := ticketFor(t, at, regrouped("perth"))

	handle(t, r, regrouped("perth", hostAlert("cccc", "firing", "web3"), hostAlert("aaaa", "firing", "web1")))

	if got := refreshed(t, at, orphan).Status; got != completeStatus {
		t.Errorf("orphaned ticket status = %d, want %d", got, completeStatus)
	}
	if got := refreshed(t, at, current).RecordedAlerts; got != "aaaa,cccc" {
		t.Errorf("current ticket RecordedAlerts = %q, want %q", got, "aaaa,cccc")
	}
}

func TestOrphanedTicketIsOnlyNotedWhenResolvedStatusDisabled(t *testing.T) {
	at := newFakeAutotask()
	opts := testOptions()
	opts.ResolvedStatus = 0
	r := newReceiver(t, at, opts)
	before := notification(hostAlert("aaaa", "firing", "web1"))
	handle(t, r, before)
	orphan := ticketFor(t, at, before)

	handle(t, r, regrouped("perth", hostAlert("aaaa", "firing", "web1")))

	orphan = refreshed(t, at, orphan)
	if orphan.Status != 1 {
		t.Errorf("status = %d, want unchanged 1", orphan.Status)
	}
	if orphan.RecordedAlerts != "" || len(at.notesFor(orphan.ID)) != 1 {
		t.Errorf("orphan should be noted and emptied: recorded %q, %d notes", orphan.RecordedAlerts, len(at.notesFor(orphan.ID)))
	}
}

func TestHandedOverOrphansAreReported(t *testing.T) {
	tests := []struct {
		name           string
		resolvedStatus int
		// moved are the alerts that move to the new alert group
		moved []receiver.Alert
		want  int
	}{
		{"completed", completeStatus, []receiver.Alert{hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")}, 1},
		{"awaiting closure", 22, []receiver.Alert{hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")}, 1},
		{"resolved status disabled", 0, []receiver.Alert{hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")}, 1},
		{"alerts still recorded", completeStatus, []receiver.Alert{hostAlert("aaaa", "firing", "web1")}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			obs := &recordingObserver{}
			opts := testOptions()
			opts.ResolvedStatus = tt.resolvedStatus
			opts.Observer = obs
			r := newReceiver(t, at, opts)
			handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")))

			handle(t, r, regrouped("perth", tt.moved...))

			if obs.handedOver != tt.want {
				t.Errorf("handed over = %d, want %d", obs.handedOver, tt.want)
			}
		})
	}
}

func TestFailedMoveIsRetried(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	before := notification(hostAlert("aaaa", "firing", "web1"))
	handle(t, r, before)
	orphan := ticketFor(t, at, before)
	after := regrouped("perth", hostAlert("aaaa", "firing", "web1"))

	at.failNext("UpdateTicket", errors.New("timeout"))
	if err := r.Handle(context.Background(), after); err == nil {
		t.Fatal("expected Handle to report the failure so Alertmanager retries")
	}
	// e.g. Alertmanager's retry, or the next repeat_interval re-send
	handle(t, r, after)

	if got := refreshed(t, at, orphan).Status; got != completeStatus {
		t.Errorf("orphaned ticket status = %d, want %d after retry", got, completeStatus)
	}
	if got := len(at.allTickets()); got != 2 {
		t.Errorf("expected two tickets, got %d", got)
	}
}

func TestDuplicateAndOverflowTicketsAreNotHandedOver(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	for _, nt := range []receiver.NewTicket{
		{ExternalID: "am:dup:0123", Status: 1, RecordedAlerts: "aaaa"},
		{ExternalID: "am:4567", Status: 1, RecordedAlerts: "overflow"},
		{ExternalID: "INC-20910-2021", Status: 1, RecordedAlerts: "aaaa"},
	} {
		if _, err := at.CreateTicket(context.Background(), nt); err != nil {
			t.Fatal(err)
		}
	}
	others := at.allTickets()

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	for _, o := range others {
		if got := refreshed(t, at, o); got.Status != 1 || got.RecordedAlerts != o.RecordedAlerts || len(at.notesFor(o.ID)) != 0 {
			t.Errorf("ticket %q should be untouched, got status %d, recorded %q, %d notes",
				o.ExternalID, got.Status, got.RecordedAlerts, len(at.notesFor(o.ID)))
		}
	}
}

func TestMoveWaitsForTheOrphanedTicketsLock(t *testing.T) {
	at := newFakeAutotask()
	locker := receiver.NewMemoryLocker()
	opts := testOptions()
	opts.HandoverLockTimeout = 50 * time.Millisecond
	r, err := receiver.New(at, fakeMatcher{"foo": {100}}, locker, opts)
	if err != nil {
		t.Fatal(err)
	}
	before := notification(hostAlert("aaaa", "firing", "web1"))
	handle(t, r, before)

	// something else is handling the orphaned ticket's group
	unlock, err := locker.Lock(context.Background(), receiver.ExternalID(before.GroupKey))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	start := time.Now()
	err = r.Handle(context.Background(), regrouped("perth", hostAlert("aaaa", "firing", "web1")))

	if err == nil || errors.Is(err, receiver.ErrPermanent) {
		t.Errorf("err = %v, want a retryable error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Handle took %v, should give up after the lock timeout", elapsed)
	}
}
