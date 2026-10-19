package receiver_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

func recurrenceOptions(obs receiver.Observer) receiver.Options {
	opts := testOptions()
	opts.TicketType = receiver.TicketTypeIncident
	opts.QueueID = 8
	opts.IssueType = 11
	opts.Category = 12
	opts.DueIn = 4 * time.Hour
	opts.Lookback = 24 * time.Hour
	opts.Observer = obs
	return opts
}

// addPrevious simulates the HostDown group's previous ticket, completed ago
// before the test time, returning its ID.
func addPrevious(at *fakeAutotask, ticketType int, problemID int64, ago time.Duration) int64 {
	return at.addTicket(fakeTicket{
		NewTicket: receiver.NewTicket{
			CompanyID: 100, Title: "Host is down", ExternalID: receiver.ExternalID(notification().GroupKey),
			Status: completeStatus, TicketType: ticketType, ProblemTicketID: problemID,
		},
		CompletedAt: testTime.Add(-ago),
	})
}

// addProblem simulates a problem ticket raised by a person, returning its ID.
func addProblem(at *fakeAutotask, completed bool) int64 {
	t := fakeTicket{NewTicket: receiver.NewTicket{CompanyID: 100, Title: "Flaky host", Status: 1, TicketType: receiver.TicketTypeProblem}}
	if completed {
		t.Status, t.CompletedAt = completeStatus, testTime.Add(-48*time.Hour)
	}
	return at.addTicket(t)
}

// currentTicket returns the HostDown group's open ticket.
func currentTicket(t *testing.T, at *fakeAutotask) fakeTicket {
	t.Helper()
	for _, tk := range at.allTickets() {
		if tk.ExternalID == receiver.ExternalID(notification().GroupKey) && tk.Status != completeStatus {
			return tk
		}
	}
	t.Fatal("no current ticket")
	return fakeTicket{}
}

// problemsRaised returns the problem tickets the receiver raised.
func problemsRaised(at *fakeAutotask, existing ...int64) []fakeTicket {
	var out []fakeTicket
	for _, tk := range at.allTickets() {
		if tk.TicketType == receiver.TicketTypeProblem && !slices.Contains(existing, tk.ID) {
			out = append(out, tk)
		}
	}
	return out
}

func noteTitled(at *fakeAutotask, id int64, title string) (receiver.NewNote, bool) {
	for _, n := range at.notesFor(id) {
		if n.Title == title {
			return n, true
		}
	}
	return receiver.NewNote{}, false
}

func TestRecurrenceRaisesProblemTicket(t *testing.T) {
	tests := []struct {
		name string
		// previous returns the previous ticket's ID, and an existing problem
		// ticket's ID or 0
		previous func(at *fakeAutotask) (previous, problem int64)
		// linksPrevious is whether the previous ticket moves to the new
		// problem ticket
		linksPrevious bool
		wantNotice    string
	}{
		{"previous incident", func(at *fakeAutotask) (int64, int64) {
			return addPrevious(at, receiver.TicketTypeIncident, 0, time.Hour), 0
		}, true, "Linked tickets: T20261017.0001, T20261017.0002"},
		{"previous ticket not an incident", func(at *fakeAutotask) (int64, int64) {
			return addPrevious(at, 5, 0, time.Hour), 0
		}, false, "NOTE: previous ticket T20261017.0001 is not an Incident"},
		{"previous ticket's problem ticket completed", func(at *fakeAutotask) (int64, int64) {
			problem := addProblem(at, true)
			return addPrevious(at, receiver.TicketTypeIncident, problem, time.Hour), problem
		}, false, "Previous ticket T20261017.0002 stays linked to completed problem ticket T20261017.0001."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			obs := &recordingObserver{}
			r := newReceiver(t, at, recurrenceOptions(obs))
			previousID, oldProblem := tt.previous(at)

			handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

			problems := problemsRaised(at, oldProblem)
			if len(problems) != 1 {
				t.Fatalf("raised %d problem tickets, want 1", len(problems))
			}
			problem := problems[0]
			current := currentTicket(t, at)
			if current.ProblemTicketID != problem.ID {
				t.Errorf("new ticket linked to %d, want problem ticket %d", current.ProblemTicketID, problem.ID)
			}

			previous := at.get(previousID)
			wantPrevious := oldProblem
			if tt.linksPrevious {
				wantPrevious = problem.ID
			}
			if previous.ProblemTicketID != wantPrevious {
				t.Errorf("previous ticket linked to %d, want %d", previous.ProblemTicketID, wantPrevious)
			}
			if previous.Status != completeStatus {
				t.Errorf("previous ticket status = %d, want it left complete", previous.Status)
			}

			if want := `Repeated alert: Host is down {tenant_id="foo"}`; problem.Title != want {
				t.Errorf("problem ticket title = %q, want %q", problem.Title, want)
			}
			for _, want := range []string{tt.wantNotice, "fired again soon after its previous ticket", "summary of aaaa"} {
				if !strings.Contains(problem.Description, want) {
					t.Errorf("problem ticket description missing %q:\n%s", want, problem.Description)
				}
			}
			if problem.ExternalID != "" || problem.RecordedAlerts != "" {
				t.Errorf("problem ticket has external ID %q and recorded alerts %q, want neither", problem.ExternalID, problem.RecordedAlerts)
			}

			note, ok := noteTitled(at, current.ID, "Linked to problem ticket "+problem.Number)
			if !ok || !strings.Contains(note.Description, problem.Number) {
				t.Errorf("new ticket notes = %+v, want one naming problem ticket %s", at.notesFor(current.ID), problem.Number)
			}
			if !slices.Equal(obs.recurrences, []string{receiver.RecurrenceRaised}) {
				t.Errorf("recurrences = %v, want [raised]", obs.recurrences)
			}
		})
	}
}

func TestRecurrenceFollowsOpenProblemTicket(t *testing.T) {
	at := newFakeAutotask()
	obs := &recordingObserver{}
	r := newReceiver(t, at, recurrenceOptions(obs))
	problemID := addProblem(at, false)
	addPrevious(at, receiver.TicketTypeIncident, problemID, time.Hour)

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	if raised := problemsRaised(at, problemID); len(raised) != 0 {
		t.Errorf("raised %d problem tickets, want none", len(raised))
	}
	current := currentTicket(t, at)
	if current.ProblemTicketID != problemID {
		t.Errorf("new ticket linked to %d, want %d", current.ProblemTicketID, problemID)
	}
	if want := "Linked to problem ticket T20261017.0001"; !strings.Contains(current.Description, want) {
		t.Errorf("description missing %q:\n%s", want, current.Description)
	}
	if notes := at.notesFor(current.ID); len(notes) != 0 {
		t.Errorf("new ticket notes = %+v, want none", notes)
	}
	if !slices.Equal(obs.recurrences, []string{receiver.RecurrenceFollowed}) {
		t.Errorf("recurrences = %v, want [followed]", obs.recurrences)
	}
}

func TestNotARecurrence(t *testing.T) {
	tests := []struct {
		name     string
		lookback time.Duration
		ago      time.Duration
	}{
		{"no lookback period", 0, time.Hour},
		{"completed before the lookback period", time.Hour, time.Hour + time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			obs := &recordingObserver{}
			opts := recurrenceOptions(obs)
			opts.Lookback = tt.lookback
			r := newReceiver(t, at, opts)
			previousID := addPrevious(at, receiver.TicketTypeIncident, 0, tt.ago)

			handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

			if raised := problemsRaised(at); len(raised) != 0 {
				t.Errorf("raised %d problem tickets, want none", len(raised))
			}
			if current := currentTicket(t, at); current.ProblemTicketID != 0 || at.get(previousID).ProblemTicketID != 0 {
				t.Error("tickets should not be linked to a problem ticket")
			}
			if len(obs.recurrences) != 0 {
				t.Errorf("recurrences = %v, want none", obs.recurrences)
			}
		})
	}
}

func TestProblemTicketFields(t *testing.T) {
	own := int64(0)
	tests := []struct {
		name        string
		company     *int64
		queue       int
		wantCompany int64
		wantQueue   int
	}{
		{"from the new ticket", nil, 0, 100, 8},
		{"problem company and queue", &own, 29, 0, 29},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			opts := recurrenceOptions(nil)
			opts.ProblemCompanyID = tt.company
			opts.ProblemQueueID = tt.queue
			r := newReceiver(t, at, opts)
			addPrevious(at, receiver.TicketTypeIncident, 0, time.Hour)

			handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

			problem := problemsRaised(at)[0]
			if problem.CompanyID != tt.wantCompany || problem.QueueID != tt.wantQueue {
				t.Errorf("company/queue = %d/%d, want %d/%d", problem.CompanyID, problem.QueueID, tt.wantCompany, tt.wantQueue)
			}
			// critical maps to priority 4
			if problem.Priority != 4 || problem.Status != 1 || problem.IssueType != 11 || problem.Category != 12 || problem.Source != 8 {
				t.Errorf("priority/status/issue type/category/source = %d/%d/%d/%d/%d, want 4/1/11/12/8",
					problem.Priority, problem.Status, problem.IssueType, problem.Category, problem.Source)
			}
			if want := testTime.Add(4 * time.Hour); problem.DueDateTime == nil || !problem.DueDateTime.Equal(want) {
				t.Errorf("DueDateTime = %v, want %v", problem.DueDateTime, want)
			}
		})
	}
}

func TestRecurrenceFailureIsLeftForAPerson(t *testing.T) {
	errDown := errors.New("autotask unavailable")
	tests := []struct {
		name string
		// setup adds the previous ticket and injects the failure
		setup       func(at *fakeAutotask)
		wantProblem bool
	}{
		{"finding the previous ticket's problem ticket", func(at *fakeAutotask) {
			addPrevious(at, receiver.TicketTypeIncident, addProblem(at, false), time.Hour)
			at.failNext("Ticket", errDown)
		}, false},
		{"linking to the previous ticket's problem ticket", func(at *fakeAutotask) {
			// not a problem ticket, so Autotask rejects the link
			addPrevious(at, receiver.TicketTypeIncident, at.addTicket(fakeTicket{NewTicket: receiver.NewTicket{TicketType: receiver.TicketTypeIncident}}), time.Hour)
		}, false},
		{"raising the problem ticket", func(at *fakeAutotask) {
			addPrevious(at, receiver.TicketTypeIncident, 0, time.Hour)
			at.failNext("CreateProblemTicket", errDown)
		}, false},
		{"linking the new ticket", func(at *fakeAutotask) {
			addPrevious(at, receiver.TicketTypeIncident, 0, time.Hour)
			at.failNext("UpdateTicket", errDown)
		}, true},
		{"linking the previous ticket", func(at *fakeAutotask) {
			id := addPrevious(at, receiver.TicketTypeIncident, 0, time.Hour)
			at.failNext("UpdateTicket:"+strconv.FormatInt(id, 10), errDown)
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			obs := &recordingObserver{}
			r := newReceiver(t, at, recurrenceOptions(obs))
			tt.setup(at)

			handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

			current := currentTicket(t, at)
			note, ok := noteTitled(at, current.ID, "Not linked to a problem ticket")
			if !ok || !strings.Contains(note.Description, "by hand") {
				t.Errorf("new ticket notes = %+v, want one explaining the link failed", at.notesFor(current.ID))
			}
			problems := problemsRaised(at)
			if raised := slices.ContainsFunc(problems, func(p fakeTicket) bool { return p.Title != "Flaky host" }); raised != tt.wantProblem {
				t.Errorf("problem ticket raised = %t, want %t", raised, tt.wantProblem)
			}
			if tt.wantProblem && !strings.Contains(note.Description, problems[len(problems)-1].Number) {
				t.Errorf("note should name the problem ticket to link to:\n%s", note.Description)
			}
			if !slices.Equal(obs.recurrences, []string{receiver.RecurrenceFailed}) {
				t.Errorf("recurrences = %v, want [failed]", obs.recurrences)
			}

			// a resend finds the new ticket as current rather than raising another
			before := len(at.allTickets())
			handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))
			if after := len(at.allTickets()); after != before {
				t.Errorf("resend created %d tickets, want none", after-before)
			}
		})
	}
}

func TestRecurrencesRequireIncidentTickets(t *testing.T) {
	opts := recurrenceOptions(nil)
	opts.TicketType = 5

	if _, err := receiver.New(newFakeAutotask(), fakeMatcher{}, receiver.NewMemoryLocker(), opts); err == nil {
		t.Error("New should reject a lookback period without the Incident ticket type")
	}
}
