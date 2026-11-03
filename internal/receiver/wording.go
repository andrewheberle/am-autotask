package receiver

import (
	"fmt"
	"strconv"
	"strings"
)

// Wording is a title and description for Autotask, already cut to the
// length limits of the fields it is written to.
type Wording struct {
	Title       string
	Description string
	// Fallbacks are the ticket templates that fell back to their built-in
	// definitions while wording it.
	Fallbacks []Fallback
}

// Fallback is a ticket template whose templates file definition was not
// used, so the built-in one was.
type Fallback struct {
	Template string
	// Err is why the definition failed, or nil when it rendered an empty
	// title.
	Err error
}

// TicketInput is what a new ticket is worded from: its alert group, and the
// facts behind its system notices.
type TicketInput struct {
	Notification Notification
	// Severity is the alert group's severity.
	Severity string

	// Ambiguous are the IDs of the companies whose company selectors all
	// matched, when the ticket is raised against the catch-all company
	// because of it.
	Ambiguous []int64
	// Previous is the alert group's previous ticket, if any.
	Previous *Ticket
	// Continues are the orphaned tickets whose alerts now belong to this
	// alert group.
	Continues []Ticket
	// Overflow is set when the alerts are too many to record.
	Overflow bool
	// Problem is the problem ticket the new ticket is raised linked to.
	Problem *Ticket
}

// NoteInput is what a change note is worded from: the alerts that changed,
// and the facts behind its system notices.
type NoteInput struct {
	Notification Notification
	// Severity is the alert group's severity.
	Severity     string
	Fired        []Alert
	Resolved     []Alert
	TicketNumber string

	// Reopened is set when the ticket was awaiting closure and is reopened.
	Reopened bool
	// Overflow is set when the alerts are too many to record.
	Overflow bool
	// Priority is how the ticket's priority follows severity, or nil when
	// it does not change.
	Priority *PriorityNotice
}

// Priority is an Autotask priority, with its label when known.
type Priority struct {
	ID    int
	Label string
}

func (p Priority) String() string {
	if p.Label == "" {
		return strconv.Itoa(p.ID)
	}
	return p.Label + " (" + strconv.Itoa(p.ID) + ")"
}

// PriorityNotice is a change to how a ticket's priority follows its alert
// group's severity.
type PriorityNotice struct {
	// Override is set when a priority override is first found; To is then
	// the priority a person chose.
	Override bool
	// Direction is [PriorityRaised] or [PriorityLowered] when the priority
	// follows severity from From to To.
	Direction string
	From, To  Priority
	// Severity is the alert group's severity the priority now follows.
	Severity string
}

// ProblemInput is what a problem ticket is worded from: the recurrence
// raising it, and the facts behind its system notices.
type ProblemInput struct {
	Notification Notification
	// Severity is the alert group's severity.
	Severity string
	// TicketTitle and TicketNumber are the new ticket's.
	TicketTitle          string
	TicketNumber         string
	PreviousTicketNumber string

	// Linked are the numbers of the tickets linked to the problem ticket.
	Linked []string
	// PreviousProblem is the completed problem ticket the previous ticket
	// stays linked to, if any.
	PreviousProblem *Ticket
	// PreviousNotIncident is set when the previous ticket is left unlinked
	// because it is not an Incident.
	PreviousNotIncident bool
}

// RecurrenceFailure is why a new ticket raised as a recurrence was not
// linked to a problem ticket.
type RecurrenceFailure struct {
	Previous Ticket
	// Problem is the problem ticket to link by hand, if one is known.
	Problem *Ticket
	Err     error
}

// MovedInput is what the note on an orphaned ticket is worded from when
// alerts move off it.
type MovedInput struct {
	Notification Notification
	Moved        []Alert
	// Current is the ticket the alerts moved to.
	Current Ticket
	// Emptied is set when no alerts remain on the orphaned ticket.
	Emptied bool
}

// Ticket words a new ticket: system notices, then its ticket templates.
func (t *Templates) Ticket(in TicketInput) Wording {
	data := ticketData(in.Notification, in.Severity)
	var w wording
	return w.done(
		truncate(w.render(t, TemplateTicketTitle, data), maxTicketTitle),
		truncate(withNotices(ticketNotices(in), w.render(t, TemplateTicketDescription, data)), maxTicketDescription),
	)
}

// ChangeNote words a change note: system notices, then its ticket
// templates.
func (t *Templates) ChangeNote(in NoteInput) Wording {
	data := noteData(in)
	var w wording
	return w.done(
		truncate(w.render(t, TemplateNoteTitle, data), maxNoteTitle),
		truncate(withNotices(noteNotices(in), w.render(t, TemplateNoteDescription, data)), maxNoteDescription),
	)
}

// ProblemTicket words a problem ticket raised for a recurrence: system
// notices, then its ticket templates.
func (t *Templates) ProblemTicket(in ProblemInput) Wording {
	data := problemData(in)
	var w wording
	return w.done(
		truncate(w.render(t, TemplateProblemTitle, data), maxTicketTitle),
		truncate(withNotices(problemNotices(in), w.render(t, TemplateProblemDescription, data)), maxTicketDescription),
	)
}

// DuplicateNote words the note on a ticket retired as a duplicate of
// current.
func (t *Templates) DuplicateNote(current Ticket) Wording {
	return noteWording("Duplicate alert group ticket",
		fmt.Sprintf("This ticket duplicates %s, which is the ticket now tracking this alert group. "+
			"Further changes will only be recorded on %s; merge or complete this ticket.", current.Number, current.Number))
}

// LinkedNote words the note on a new ticket linked to a newly raised
// problem ticket.
func (t *Templates) LinkedNote(problem Ticket) Wording {
	return noteWording("Linked to problem ticket "+problem.Number, linkedNotice(problem))
}

// RecurrenceFailedNote words the note on a new ticket explaining why it is
// not linked to a problem ticket.
func (t *Templates) RecurrenceFailedNote(f RecurrenceFailure) Wording {
	var b strings.Builder
	fmt.Fprintf(&b, "NOTE: this alert group fired again soon after its previous ticket %s was completed, but linking to a problem ticket failed: %v\n\n", f.Previous.Number, f.Err)
	if f.Problem != nil {
		fmt.Fprintf(&b, "Link this ticket, and %s if it is an Incident, to problem ticket %s by hand.", f.Previous.Number, f.Problem.Number)
	} else {
		fmt.Fprintf(&b, "Link this ticket, and %s if it is an Incident, to a problem ticket by hand.", f.Previous.Number)
	}
	return noteWording("Not linked to a problem ticket", b.String())
}

// MovedNote words the note on an orphaned ticket when alerts move off it.
func (t *Templates) MovedNote(in MovedInput) Wording {
	title := fmt.Sprintf("%d alerts moved to %s", len(in.Moved), in.Current.Number)
	switch {
	case in.Emptied:
		title = fmt.Sprintf("All alerts moved to %s", in.Current.Number)
	case len(in.Moved) == 1:
		title = fmt.Sprintf("1 alert moved to %s", in.Current.Number)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "These alerts now belong to alert group %s, tracked on %s. "+
		"This usually follows a change to Alertmanager's routes or group_by.\n", formatLabels(in.Notification.GroupLabels, nil), in.Current.Number)
	if in.Emptied {
		fmt.Fprintf(&b, "No alerts remain on this ticket; further changes are recorded on %s.\n", in.Current.Number)
	}
	fmt.Fprintf(&b, "\nMoved (%d):\n", len(in.Moved))
	writeAlerts(&b, in.Notification, in.Moved, t.loc)

	return noteWording(title, b.String())
}

// noteWording is a system note cut to the note field limits.
func noteWording(title, description string) Wording {
	return Wording{Title: truncate(title, maxNoteTitle), Description: truncate(description, maxNoteDescription)}
}

// wording collects the fallbacks of the ticket templates rendered for one
// [Wording].
type wording struct {
	fallbacks []Fallback
}

// render executes a ticket template and tidies the result. A failing
// definition from the templates file falls back to the built-in one, and so
// does a title that renders empty.
func (w *wording) render(t *Templates, name string, data any) string {
	out, err := t.render(name, data)
	if err != nil {
		w.fallbacks = append(w.fallbacks, Fallback{Template: name, Err: err})
	}
	out = tidy(name, out)
	if out == "" && strings.HasSuffix(name, ".title") {
		w.fallbacks = append(w.fallbacks, Fallback{Template: name})
		builtin, _ := t.renderBuiltin(name, data)
		out = tidy(name, builtin)
	}
	return out
}

func (w *wording) done(title, description string) Wording {
	return Wording{Title: title, Description: description, Fallbacks: w.fallbacks}
}

// withNotices puts system notices ahead of a rendered description, where a
// template cannot leave them out (ADR-0005).
func withNotices(notices []string, body string) string {
	if len(notices) == 0 {
		return body
	}
	return strings.Join(notices, "\n\n") + "\n\n" + body
}

const overflowNotice = "NOTE: This alert group has too many alerts to track individually; " +
	"further alerts firing or resolving will not be noted until the whole group resolves."

// ticketNotices are the system notices for a new ticket.
func ticketNotices(in TicketInput) []string {
	var notices []string
	if len(in.Ambiguous) > 0 {
		notices = append(notices, fmt.Sprintf("NOTE: this alert group matched the company selectors of more than one company (IDs %s) so it was raised against the catch-all company. Correct the company selectors so only one company matches.",
			joinIDs(in.Ambiguous)))
	}
	if in.Previous != nil {
		notices = append(notices, "Previous ticket for this alert group: "+in.Previous.Number)
	}
	if len(in.Continues) > 0 {
		notices = append(notices, fmt.Sprintf("Continues %s: alerts recorded there now belong to this alert group, usually after a change to Alertmanager's routes or group_by.",
			joinNumbers(in.Continues)))
	}
	if in.Overflow {
		notices = append(notices, overflowNotice)
	}
	if in.Problem != nil {
		notices = append(notices, linkedNotice(*in.Problem))
	}
	return notices
}

// noteNotices are the system notices for a change note: reopening,
// overflow, and any priority change.
func noteNotices(in NoteInput) []string {
	var notices []string
	if in.Reopened {
		notices = append(notices, "This ticket was awaiting closure and has been reopened because its alert group fired again.")
	}
	if in.Overflow {
		notices = append(notices, overflowNotice)
	}
	if p := in.Priority; p != nil {
		switch {
		case p.Override:
			notices = append(notices, "Priority was changed manually to "+p.To.String()+", so it will no longer follow alert severity.")
		case p.Direction != "":
			severity := p.Severity
			if severity == "" {
				severity = "unset"
			}
			notices = append(notices, "Priority "+p.Direction+" from "+p.From.String()+" to "+p.To.String()+": the highest firing severity is now "+severity+".")
		}
	}
	return notices
}

// linkedNotice is the system notice on a new ticket linked to a problem
// ticket.
func linkedNotice(problem Ticket) string {
	return fmt.Sprintf("Linked to problem ticket %s: this alert group fired again soon after its previous ticket was completed.", problem.Number)
}

// problemNotices are the system notices for a new problem ticket, naming the
// tickets it links and explaining a previous ticket left unlinked.
func problemNotices(in ProblemInput) []string {
	var notices []string
	switch len(in.Linked) {
	case 0:
	case 1:
		notices = append(notices, "Linked ticket: "+in.Linked[0])
	default:
		notices = append(notices, "Linked tickets: "+strings.Join(in.Linked, ", "))
	}
	switch {
	case in.PreviousProblem != nil:
		notices = append(notices, fmt.Sprintf("Previous ticket %s stays linked to completed problem ticket %s.", in.PreviousTicketNumber, in.PreviousProblem.Number))
	case in.PreviousNotIncident:
		notices = append(notices, fmt.Sprintf("NOTE: previous ticket %s is not an Incident, so it could not be linked to this problem ticket.", in.PreviousTicketNumber))
	}
	return notices
}
