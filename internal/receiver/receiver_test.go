package receiver_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

const catchAll = 1

func testOptions() receiver.Options {
	return receiver.Options{
		CatchAllCompanyID: catchAll,
		Priorities:        map[string]int{"critical": 4, "warning": 2},
		DefaultPriority:   3,
		Status:            1,
		TicketType:        5,
		Source:            8,
		ResolvedStatus:    completeStatus,
		NoteType:          1,
		NotePublish:       2,
		Now:               func() time.Time { return testTime },
	}
}

func newReceiver(t *testing.T, at *fakeAutotask, opts receiver.Options) *receiver.Receiver {
	t.Helper()
	r, err := receiver.New(at, fakeMatcher{"foo": {100}, "both": {100, 200}, "rgi": {catchAll}}, receiver.NewMemoryLocker(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func handle(t *testing.T, r *receiver.Receiver, n receiver.Notification) {
	t.Helper()
	if err := r.Handle(context.Background(), n); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func onlyTicket(t *testing.T, at *fakeAutotask) fakeTicket {
	t.Helper()
	tickets := at.allTickets()
	if len(tickets) != 1 {
		t.Fatalf("expected exactly one ticket, got %d", len(tickets))
	}
	return tickets[0]
}

func TestFirstFiringNotificationCreatesTicketAgainstMatchedCompany(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	ticket := onlyTicket(t, at)
	if ticket.CompanyID != 100 {
		t.Errorf("CompanyID = %d, want 100", ticket.CompanyID)
	}
	if want := `HostDown {tenant_id="foo"}`; ticket.Title != want {
		t.Errorf("Title = %q, want %q", ticket.Title, want)
	}
	if ticket.Priority != 4 || ticket.Status != 1 || ticket.TicketType != 5 || ticket.Source != 8 {
		t.Errorf("priority/status/type/source = %d/%d/%d/%d, want 4/1/5/8",
			ticket.Priority, ticket.Status, ticket.TicketType, ticket.Source)
	}
	if ticket.RecordedAlerts != "aaaa" {
		t.Errorf("RecordedAlerts = %q, want %q", ticket.RecordedAlerts, "aaaa")
	}
	for _, want := range []string{"Host is down", `instance="web1"`, "summary of aaaa", "http://alertmanager:9093"} {
		if !strings.Contains(ticket.Description, want) {
			t.Errorf("Description missing %q:\n%s", want, ticket.Description)
		}
	}
}

func TestExternalIDIsDerivedFromGroupKey(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	// first 40 hex chars of sha256 of the group key used by notification(),
	// computed independently with sha256sum
	if got, want := onlyTicket(t, at).ExternalID, "am:5ba0db306c47453f6f94819a17eb6e01a15a77f2"; got != want {
		t.Errorf("ExternalID = %q, want %q", got, want)
	}
}

func TestUnmatchedGroupGoesToCatchAllCompany(t *testing.T) {
	tests := []struct {
		name     string
		catchAll int64
	}{
		{"company", catchAll},
		// company 0 is the Autotask instance's own company
		{"own company", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			opts := testOptions()
			opts.CatchAllCompanyID = tt.catchAll
			r := newReceiver(t, at, opts)

			n := notification(hostAlert("aaaa", "firing", "web1"))
			n.GroupLabels["tenant_id"] = "unknown"
			handle(t, r, n)

			if got := onlyTicket(t, at).CompanyID; got != tt.catchAll {
				t.Errorf("CompanyID = %d, want catch-all %d", got, tt.catchAll)
			}
		})
	}
}

func TestAmbiguousGroupGoesToCatchAllCompanyAndSaysWhy(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())

	n := notification(hostAlert("aaaa", "firing", "web1"))
	n.GroupLabels["tenant_id"] = "both"
	handle(t, r, n)

	ticket := onlyTicket(t, at)
	if ticket.CompanyID != catchAll {
		t.Errorf("CompanyID = %d, want catch-all %d", ticket.CompanyID, catchAll)
	}
	if !strings.Contains(ticket.Description, "100") || !strings.Contains(ticket.Description, "200") {
		t.Errorf("Description should name the ambiguous companies 100 and 200:\n%s", ticket.Description)
	}
}

func TestUnmappedSeverityUsesDefaultPriority(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())

	n := notification(hostAlert("aaaa", "firing", "web1"))
	n.CommonLabels["severity"] = "info"
	handle(t, r, n)

	if got := onlyTicket(t, at).Priority; got != 3 {
		t.Errorf("Priority = %d, want default 3", got)
	}
}

func TestOptionalTicketFieldsAreSentWhenConfigured(t *testing.T) {
	at := newFakeAutotask()
	opts := testOptions()
	opts.QueueID, opts.IssueType, opts.SubIssueType, opts.Category, opts.WorkType = 8, 14, 140, 109, 29682801
	opts.DueIn = 4 * time.Hour
	r := newReceiver(t, at, opts)

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	ticket := onlyTicket(t, at)
	if ticket.QueueID != 8 || ticket.IssueType != 14 || ticket.SubIssueType != 140 || ticket.Category != 109 || ticket.WorkType != 29682801 {
		t.Errorf("queue/issue/subissue/category/worktype = %d/%d/%d/%d/%d, want 8/14/140/109/29682801",
			ticket.QueueID, ticket.IssueType, ticket.SubIssueType, ticket.Category, ticket.WorkType)
	}
	if ticket.DueDateTime == nil || !ticket.DueDateTime.Equal(testTime.Add(4*time.Hour)) {
		t.Errorf("DueDateTime = %v, want %v", ticket.DueDateTime, testTime.Add(4*time.Hour))
	}
}

func TestOptionalTicketFieldsAreOmittedByDefault(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	ticket := onlyTicket(t, at)
	if ticket.QueueID != 0 || ticket.IssueType != 0 || ticket.SubIssueType != 0 || ticket.Category != 0 || ticket.WorkType != 0 || ticket.DueDateTime != nil {
		t.Errorf("optional fields should be unset, got %+v", ticket.NewTicket)
	}
}
