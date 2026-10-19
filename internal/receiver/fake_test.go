package receiver_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

const completeStatus = 5

// fakeAutotask is an in-memory stand-in for the Autotask API.
type fakeAutotask struct {
	mu      sync.Mutex
	nextID  int64
	tickets []*fakeTicket
	notes   []receiver.NewNote

	// failures injects an error the next time the named operation is called
	failures map[string]error
}

type fakeTicket struct {
	receiver.NewTicket
	ID          int64
	Number      string
	CompletedAt time.Time
}

// errRejected is Autotask refusing a request as invalid.
var errRejected = fmt.Errorf("autotask rejected the request: %w", receiver.ErrPermanent)

func newFakeAutotask() *fakeAutotask {
	return &fakeAutotask{nextID: 1000, failures: map[string]error{}}
}

func (f *fakeAutotask) failNext(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[op] = err
}

// fail returns the error injected for op, or for op on one ticket as
// "op:id", once.
func (f *fakeAutotask) fail(op string, id ...int64) error {
	keys := []string{op}
	for _, i := range id {
		keys = append(keys, fmt.Sprintf("%s:%d", op, i))
	}
	for _, key := range keys {
		if err, ok := f.failures[key]; ok {
			delete(f.failures, key)
			return err
		}
	}
	return nil
}

func (f *fakeAutotask) toTicket(t *fakeTicket) receiver.Ticket {
	return receiver.Ticket{
		ID: t.ID, Number: t.Number, Status: t.Status, ExternalID: t.ExternalID, RecordedAlerts: t.RecordedAlerts,
		Priority: t.Priority, AppliedPriority: t.AppliedPriority,
		Type: t.TicketType, ProblemTicketID: t.ProblemTicketID, CompletedAt: t.CompletedAt,
	}
}

func (f *fakeAutotask) Ticket(_ context.Context, id int64) (*receiver.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Ticket"); err != nil {
		return nil, err
	}

	t := f.ticket(id)
	if t == nil {
		return nil, nil
	}
	tt := f.toTicket(t)
	return &tt, nil
}

// checkProblemLink applies Autotask's rules for linking a ticket of type
// ticketType to problemID.
func (f *fakeAutotask) checkProblemLink(ticketType int, problemID int64) error {
	if problemID == 0 {
		return nil
	}
	if ticketType != receiver.TicketTypeIncident {
		return fmt.Errorf("ticketType must be Incident to link a problem ticket: %w", errRejected)
	}
	p := f.ticket(problemID)
	if p == nil || p.TicketType != receiver.TicketTypeProblem {
		return fmt.Errorf("ticket %d is not a problem ticket: %w", problemID, errRejected)
	}
	return nil
}

func (f *fakeAutotask) OpenAlertTickets(_ context.Context) ([]receiver.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("OpenAlertTickets"); err != nil {
		return nil, err
	}

	var out []receiver.Ticket
	for _, t := range f.tickets {
		if strings.HasPrefix(t.ExternalID, "am:") && t.Status != completeStatus {
			out = append(out, f.toTicket(t))
		}
	}
	return out, nil
}

func (f *fakeAutotask) OpenTickets(_ context.Context, externalID string) ([]receiver.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("OpenTickets"); err != nil {
		return nil, err
	}

	var out []receiver.Ticket
	for _, t := range f.tickets {
		if t.ExternalID == externalID && t.Status != completeStatus {
			out = append(out, f.toTicket(t))
		}
	}
	return out, nil
}

func (f *fakeAutotask) PreviousTicket(_ context.Context, externalID string) (*receiver.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var prev *receiver.Ticket
	for _, t := range f.tickets {
		if t.ExternalID == externalID && t.Status == completeStatus {
			tt := f.toTicket(t)
			prev = &tt
		}
	}
	return prev, nil
}

func (f *fakeAutotask) CreateTicket(_ context.Context, t receiver.NewTicket) (receiver.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CreateTicket"); err != nil {
		return receiver.Ticket{}, err
	}
	if t.TicketType == receiver.TicketTypeProblem {
		if err := f.fail("CreateProblemTicket"); err != nil {
			return receiver.Ticket{}, err
		}
	}
	if err := f.checkProblemLink(t.TicketType, t.ProblemTicketID); err != nil {
		return receiver.Ticket{}, err
	}

	f.nextID++
	ft := &fakeTicket{NewTicket: t, ID: f.nextID, Number: fmt.Sprintf("T20261017.%04d", f.nextID-1000)}
	f.tickets = append(f.tickets, ft)
	return f.toTicket(ft), nil
}

func (f *fakeAutotask) CreateNote(_ context.Context, n receiver.NewNote) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CreateNote"); err != nil {
		return err
	}

	f.notes = append(f.notes, n)
	return nil
}

func (f *fakeAutotask) UpdateTicket(_ context.Context, id int64, u receiver.TicketUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("UpdateTicket", id); err != nil {
		return err
	}

	t := f.ticket(id)
	if t == nil {
		return fmt.Errorf("no ticket %d", id)
	}
	if u.ProblemTicketID != nil {
		if err := f.checkProblemLink(t.TicketType, *u.ProblemTicketID); err != nil {
			return err
		}
		t.ProblemTicketID = *u.ProblemTicketID
	}
	if u.RecordedAlerts != nil {
		t.RecordedAlerts = *u.RecordedAlerts
	}
	if u.Status != nil {
		t.Status = *u.Status
	}
	if u.ExternalID != nil {
		t.ExternalID = *u.ExternalID
	}
	if u.Priority != nil {
		t.Priority = *u.Priority
	}
	if u.AppliedPriority != nil {
		t.AppliedPriority = *u.AppliedPriority
	}
	return nil
}

// get returns a copy of the ticket with this ID.
func (f *fakeAutotask) get(id int64) fakeTicket {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.ticket(id)
}

func (f *fakeAutotask) ticket(id int64) *fakeTicket {
	for _, t := range f.tickets {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// test helpers

// closeTicket simulates a technician completing a ticket in Autotask.
func (f *fakeAutotask) closeTicket(id int64) {
	f.closeTicketAt(id, testTime)
}

// closeTicketAt simulates a technician completing a ticket at a given time.
func (f *fakeAutotask) closeTicketAt(id int64, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.ticket(id)
	t.Status = completeStatus
	t.CompletedAt = at
}

// setPriority simulates a technician changing a ticket's priority.
func (f *fakeAutotask) setPriority(id int64, priority int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ticket(id).Priority = priority
}

// setAppliedPriority simulates a ticket's applied priority UDF, e.g. one
// opened before priority followed severity.
func (f *fakeAutotask) setAppliedPriority(id int64, applied string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ticket(id).AppliedPriority = applied
}

// addTicket simulates a ticket created outside the receiver, returning its
// ID.
func (f *fakeAutotask) addTicket(t fakeTicket) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	t.ID = f.nextID
	t.Number = fmt.Sprintf("T20261017.%04d", f.nextID-1000)
	f.tickets = append(f.tickets, &t)
	return t.ID
}

func (f *fakeAutotask) allTickets() []fakeTicket {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeTicket, 0, len(f.tickets))
	for _, t := range f.tickets {
		out = append(out, *t)
	}
	return out
}

func (f *fakeAutotask) notesFor(id int64) []receiver.NewNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []receiver.NewNote
	for _, n := range f.notes {
		if n.TicketID == id {
			out = append(out, n)
		}
	}
	return out
}

// fakeMatcher stands in for the company selector cache.
type fakeMatcher map[string][]int64

func (m fakeMatcher) Match(_ context.Context, groupLabels map[string]string) []int64 {
	return slices.Clone(m[groupLabels["tenant_id"]])
}

var testTime = time.Date(2026, 10, 17, 4, 0, 0, 0, time.UTC)

func alert(fingerprint, status string, labels map[string]string) receiver.Alert {
	a := receiver.Alert{
		Status:       status,
		Labels:       labels,
		Annotations:  map[string]string{"summary": "summary of " + fingerprint},
		StartsAt:     testTime.Add(-10 * time.Minute),
		GeneratorURL: "http://prometheus/graph?g0.expr=" + fingerprint,
		Fingerprint:  fingerprint,
	}
	if status == "resolved" {
		a.EndsAt = testTime
	}
	return a
}

// notification builds a notification for the "foo" tenant's HostDown group.
func notification(alerts ...receiver.Alert) receiver.Notification {
	status := "resolved"
	for _, a := range alerts {
		if a.Status == "firing" {
			status = "firing"
		}
	}

	return receiver.Notification{
		Version:           "4",
		GroupKey:          `{}/{severity="critical"}:{alertname="HostDown", tenant_id="foo"}`,
		Status:            status,
		Receiver:          "autotask",
		GroupLabels:       map[string]string{"alertname": "HostDown", "tenant_id": "foo"},
		CommonLabels:      map[string]string{"alertname": "HostDown", "tenant_id": "foo", "severity": "critical"},
		CommonAnnotations: map[string]string{"summary": "Host is down"},
		ExternalURL:       "http://alertmanager:9093",
		Alerts:            alerts,
	}
}

func hostAlert(fingerprint, status, instance string) receiver.Alert {
	return alert(fingerprint, status, map[string]string{
		"alertname": "HostDown", "tenant_id": "foo", "severity": "critical", "instance": instance,
	})
}
