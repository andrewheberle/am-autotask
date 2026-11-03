// Package receivertest checks that an adapter of [receiver.Autotask] behaves
// as the receiver relies on, so the in-memory fake its tests use and the
// Autotask client are held to the same contract.
package receivertest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// Harness is an adapter of [receiver.Autotask] under test, with what only a
// person can do in Autotask. The adapter must record applied priorities, as
// it does when ticket priority follows severity.
type Harness struct {
	Autotask receiver.Autotask
	// Complete completes the ticket with this ID at the given time, as a
	// person or workflow rule does in Autotask.
	Complete func(t *testing.T, id int64, at time.Time)
}

// completedAt is when tickets are completed; whole seconds, as Autotask
// stores completion times to the millisecond.
var completedAt = time.Date(2026, 10, 17, 4, 0, 0, 0, time.UTC)

const statusNew = 1

// TestAutotask runs the contract against the adapter newHarness returns,
// which must start with no tickets.
func TestAutotask(t *testing.T, newHarness func(t *testing.T) Harness) {
	tests := []struct {
		name string
		test func(t *testing.T, h Harness)
	}{
		{"open tickets are those of the external ID that are not complete, oldest first", openTickets},
		{"previous ticket is the most recent completed one", previousTicket},
		{"a ticket that does not exist is nil", missingTicket},
		{"created ticket has a number and its fields", createdTicket},
		{"update changes only the fields given", updateOnlyGiven},
		{"tickets link to problem tickets", problemLink},
		{"open alert tickets carry only some fields", openAlertTickets},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t, newHarness(t))
		})
	}
}

func alertTicket(externalID string) receiver.NewTicket {
	return receiver.NewTicket{
		CompanyID: 100, Title: "Host is down", Description: "web1", ExternalID: externalID,
		Priority: 4, Status: statusNew, TicketType: receiver.TicketTypeIncident,
		RecordedAlerts: "aaaa,bbbb", AppliedPriority: "4",
	}
}

func create(t *testing.T, h Harness, nt receiver.NewTicket) receiver.Ticket {
	t.Helper()
	created, err := h.Autotask.CreateTicket(context.Background(), nt)
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return created
}

func get(t *testing.T, h Harness, id int64) receiver.Ticket {
	t.Helper()
	got, err := h.Autotask.Ticket(context.Background(), id)
	if err != nil {
		t.Fatalf("Ticket(%d): %v", id, err)
	}
	if got == nil {
		t.Fatalf("Ticket(%d) = nil, want the ticket", id)
	}
	return *got
}

func update(t *testing.T, h Harness, id int64, u receiver.TicketUpdate) {
	t.Helper()
	if err := h.Autotask.UpdateTicket(context.Background(), id, u); err != nil {
		t.Fatalf("UpdateTicket(%d): %v", id, err)
	}
}

func ids(tickets []receiver.Ticket) []int64 {
	out := make([]int64, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, t.ID)
	}
	return out
}

func openTickets(t *testing.T, h Harness) {
	first := create(t, h, alertTicket("am:one"))
	create(t, h, alertTicket("am:two"))
	second := create(t, h, alertTicket("am:one"))
	third := create(t, h, alertTicket("am:one"))
	h.Complete(t, second.ID, completedAt)

	open, err := h.Autotask.OpenTickets(context.Background(), "am:one")
	if err != nil {
		t.Fatalf("OpenTickets: %v", err)
	}
	if got, want := ids(open), []int64{first.ID, third.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("OpenTickets = %v, want %v", got, want)
	}
}

func previousTicket(t *testing.T, h Harness) {
	ctx := context.Background()
	if prev, err := h.Autotask.PreviousTicket(ctx, "am:one"); err != nil || prev != nil {
		t.Errorf("PreviousTicket with none completed = %+v, %v; want nil", prev, err)
	}

	older := create(t, h, alertTicket("am:one"))
	newer := create(t, h, alertTicket("am:one"))
	create(t, h, alertTicket("am:one"))
	h.Complete(t, older.ID, completedAt.Add(-time.Hour))
	h.Complete(t, newer.ID, completedAt)

	prev, err := h.Autotask.PreviousTicket(ctx, "am:one")
	if err != nil {
		t.Fatalf("PreviousTicket: %v", err)
	}
	if prev == nil || prev.ID != newer.ID || !prev.CompletedAt.Equal(completedAt) || prev.Type != receiver.TicketTypeIncident {
		t.Errorf("PreviousTicket = %+v, want ticket %d, an Incident completed at %v", prev, newer.ID, completedAt)
	}
}

func missingTicket(t *testing.T, h Harness) {
	got, err := h.Autotask.Ticket(context.Background(), 999999)
	if err != nil || got != nil {
		t.Errorf("Ticket(999999) = %+v, %v; want nil, nil", got, err)
	}
}

func createdTicket(t *testing.T, h Harness) {
	created := create(t, h, alertTicket("am:one"))
	if created.ID == 0 || created.Number == "" {
		t.Fatalf("CreateTicket = %+v, want an ID and number", created)
	}

	want := receiver.Ticket{
		ID: created.ID, Number: created.Number, Status: statusNew, ExternalID: "am:one",
		RecordedAlerts: "aaaa,bbbb", Priority: 4, AppliedPriority: "4", Type: receiver.TicketTypeIncident,
	}
	if created != want {
		t.Errorf("CreateTicket = %+v, want %+v", created, want)
	}
	if got := get(t, h, created.ID); got != want {
		t.Errorf("Ticket = %+v, want %+v", got, want)
	}
}

func updateOnlyGiven(t *testing.T, h Harness) {
	created := create(t, h, alertTicket("am:one"))

	recorded := "cccc"
	update(t, h, created.ID, receiver.TicketUpdate{RecordedAlerts: &recorded})
	want := created
	want.RecordedAlerts = recorded
	if got := get(t, h, created.ID); got != want {
		t.Errorf("after updating recorded alerts, Ticket = %+v, want %+v", got, want)
	}

	status, priority, applied, externalID := 8, 2, "override", "am:dup:one"
	update(t, h, created.ID, receiver.TicketUpdate{Status: &status, Priority: &priority, AppliedPriority: &applied, ExternalID: &externalID})
	want.Status, want.Priority, want.AppliedPriority, want.ExternalID = status, priority, applied, externalID
	if got := get(t, h, created.ID); got != want {
		t.Errorf("after updating the rest, Ticket = %+v, want %+v", got, want)
	}
}

func problemLink(t *testing.T, h Harness) {
	problem := create(t, h, receiver.NewTicket{
		CompanyID: 100, Title: "Repeated alert", Description: "web1",
		Priority: 4, Status: statusNew, TicketType: receiver.TicketTypeProblem,
	})
	if got := get(t, h, problem.ID); got.Type != receiver.TicketTypeProblem || got.ExternalID != "" {
		t.Errorf("problem ticket = %+v, want a Problem with no external ID", got)
	}

	linked := alertTicket("am:one")
	linked.ProblemTicketID = problem.ID
	if got := create(t, h, linked); got.ProblemTicketID != problem.ID {
		t.Errorf("ticket created linked = %+v, want linked to %d", got, problem.ID)
	}

	later := create(t, h, alertTicket("am:two"))
	update(t, h, later.ID, receiver.TicketUpdate{ProblemTicketID: &problem.ID})
	if got := get(t, h, later.ID); got.ProblemTicketID != problem.ID || got.RecordedAlerts != later.RecordedAlerts {
		t.Errorf("ticket linked by update = %+v, want linked to %d and otherwise unchanged", got, problem.ID)
	}
}

func openAlertTickets(t *testing.T, h Harness) {
	problem := create(t, h, receiver.NewTicket{CompanyID: 100, Title: "p", Description: "p", Priority: 4, Status: statusNew, TicketType: receiver.TicketTypeProblem})
	nt := alertTicket("am:one")
	nt.ProblemTicketID = problem.ID
	one := create(t, h, nt)
	two := create(t, h, alertTicket("am:two"))
	completed := create(t, h, alertTicket("am:three"))
	h.Complete(t, completed.ID, completedAt)

	got, err := h.Autotask.OpenAlertTickets(context.Background())
	if err != nil {
		t.Fatalf("OpenAlertTickets: %v", err)
	}
	// only the fields documented on [receiver.Autotask.OpenAlertTickets]
	partial := func(c receiver.Ticket) receiver.Ticket {
		return receiver.Ticket{ID: c.ID, Number: c.Number, Status: c.Status, ExternalID: c.ExternalID, RecordedAlerts: c.RecordedAlerts}
	}
	if want := []receiver.Ticket{partial(one), partial(two)}; !reflect.DeepEqual(got, want) {
		t.Errorf("OpenAlertTickets =\n%+v\nwant only open alert tickets, with only some fields:\n%+v", got, want)
	}
}
