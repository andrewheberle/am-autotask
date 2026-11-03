package receiver

import (
	"context"
	"errors"
	"fmt"
)

// Outcomes of a recurrence, reported to [Observer.Recurrence].
const (
	// RecurrenceFollowed is a new ticket linked to its previous ticket's
	// open problem ticket.
	RecurrenceFollowed = "followed"
	// RecurrenceRaised is a new problem ticket raised for a recurrence.
	RecurrenceRaised = "raised"
	// RecurrenceFailed is a recurrence left for a person to link.
	RecurrenceFailed = "failed"
)

// recurrence is a new ticket raised within the lookback period of its alert
// group's previous ticket being completed (ADR-0006), with what is known of
// the previous ticket's problem ticket.
type recurrence struct {
	previous Ticket
	// previousProblem is the problem ticket the previous ticket is linked
	// to, or nil when it is linked to none
	previousProblem *Ticket
	// open is previousProblem when it is still open, for the new ticket to
	// follow
	open *Ticket
	// lookupErr is why the previous ticket's problem ticket could not be
	// found
	lookupErr error
}

// recurrence returns the recurrence a new ticket would be when previous, the
// alert group's previous ticket, was completed within the lookback period,
// otherwise nil. A failure to find the previous ticket's problem ticket is
// recorded on the recurrence rather than returned, as a ticket must still be
// raised.
func (r *Receiver) recurrence(ctx context.Context, previous *Ticket) *recurrence {
	if r.opts.Lookback <= 0 || previous == nil || previous.CompletedAt.IsZero() ||
		r.opts.Now().Sub(previous.CompletedAt) > r.opts.Lookback {
		return nil
	}

	rec := &recurrence{previous: *previous}
	if previous.ProblemTicketID == 0 {
		return rec
	}
	problem, err := r.at.Ticket(ctx, previous.ProblemTicketID)
	if err != nil {
		rec.lookupErr = fmt.Errorf("finding problem ticket %d of previous ticket %s: %w", previous.ProblemTicketID, previous.Number, err)
		return rec
	}
	rec.previousProblem = problem
	if problem != nil && problem.CompletedAt.IsZero() {
		rec.open = problem
	}
	return rec
}

// linkPrevious reports whether the previous ticket can be linked to a new
// problem ticket: Autotask only links incidents, and a ticket already linked
// to a completed problem ticket stays there.
func (rec *recurrence) linkPrevious() bool {
	return rec.previous.Type == TicketTypeIncident && rec.previousProblem == nil
}

// raiseTicket creates t, worded from in, as a recurrence linked to a problem
// ticket when in.Previous was completed within the lookback period: the
// previous ticket's problem ticket while that is open, otherwise a new one.
// Only a failure to create the ticket is returned. Failures to link it are
// logged and explained on it instead, as a retry would find it as current
// and never link it.
func (r *Receiver) raiseTicket(ctx context.Context, in TicketInput, t NewTicket) (Ticket, error) {
	rec := r.recurrence(ctx, in.Previous)
	if rec == nil {
		return r.at.CreateTicket(ctx, t)
	}

	if rec.lookupErr == nil && rec.open != nil {
		linked := t
		linked.ProblemTicketID = rec.open.ID
		in.Problem = rec.open
		// the same templates and data as the unlinked wording, whose
		// fallbacks have already been reported
		linked.Description = r.opts.Templates.Ticket(in).Description
		created, err := r.at.CreateTicket(ctx, linked)
		switch {
		case err == nil:
			r.report(ctx, eventRecurrence{outcome: RecurrenceFollowed})
			return created, nil
		case !errors.Is(err, ErrPermanent):
			return Ticket{}, err
		}

		// Autotask rejected the link, perhaps because the problem ticket
		// holds as many incidents as it allows; the ticket matters more
		linkErr := fmt.Errorf("linking to problem ticket %s: %w", rec.open.Number, err)
		created, err = r.at.CreateTicket(ctx, t)
		if err != nil {
			return Ticket{}, err
		}
		r.recurrenceFailed(ctx, created, RecurrenceFailure{Previous: rec.previous, Problem: rec.open, Err: linkErr})
		return created, nil
	}

	created, err := r.at.CreateTicket(ctx, t)
	if err != nil {
		return Ticket{}, err
	}
	if rec.lookupErr != nil {
		r.recurrenceFailed(ctx, created, RecurrenceFailure{Previous: rec.previous, Err: rec.lookupErr})
		return created, nil
	}
	r.linkToNewProblem(ctx, rec, in, t, created)
	return created, nil
}

// linkToNewProblem raises a problem ticket for created, a new ticket raised
// as t from in, and links created, and the previous ticket when it can be,
// to it.
func (r *Receiver) linkToNewProblem(ctx context.Context, rec *recurrence, in TicketInput, t NewTicket, created Ticket) {
	problem, err := r.raiseProblem(ctx, rec, in, t, created)
	if err != nil {
		r.recurrenceFailed(ctx, created, RecurrenceFailure{Previous: rec.previous, Err: err})
		return
	}

	if err := r.at.UpdateTicket(ctx, created.ID, TicketUpdate{ProblemTicketID: &problem.ID}); err != nil {
		r.recurrenceFailed(ctx, created, RecurrenceFailure{Previous: rec.previous, Problem: &problem,
			Err: fmt.Errorf("linking %s to problem ticket %s: %w", created.Number, problem.Number, err)})
		return
	}
	if rec.linkPrevious() {
		if err := r.at.UpdateTicket(ctx, rec.previous.ID, TicketUpdate{ProblemTicketID: &problem.ID}); err != nil {
			r.recurrenceFailed(ctx, created, RecurrenceFailure{Previous: rec.previous, Problem: &problem,
				Err: fmt.Errorf("linking previous ticket %s to problem ticket %s: %w", rec.previous.Number, problem.Number, err)})
			return
		}
	}

	if err := r.note(ctx, created.ID, r.opts.Templates.LinkedNote(problem)); err != nil {
		r.opts.Logger.ErrorContext(ctx, "adding problem ticket note failed", "ticket", created.Number, "problem", problem.Number, "error", err)
	}
	r.report(ctx, eventRecurrence{outcome: RecurrenceRaised})
}

// raiseProblem creates a problem ticket for a recurrence, taking its fields
// from t, the new ticket as raised from in.
func (r *Receiver) raiseProblem(ctx context.Context, rec *recurrence, in TicketInput, t NewTicket, created Ticket) (Ticket, error) {
	pin := ProblemInput{
		Notification:         in.Notification,
		Severity:             in.Severity,
		TicketTitle:          t.Title,
		TicketNumber:         created.Number,
		PreviousTicketNumber: rec.previous.Number,
		Linked:               []string{created.Number},
	}
	switch {
	case rec.linkPrevious():
		pin.Linked = []string{rec.previous.Number, created.Number}
	case rec.previousProblem != nil:
		pin.PreviousProblem = rec.previousProblem
	default:
		pin.PreviousNotIncident = true
	}
	companyID := t.CompanyID
	if r.opts.ProblemCompanyID != nil {
		companyID = *r.opts.ProblemCompanyID
	}
	queueID := t.QueueID
	if r.opts.ProblemQueueID != 0 {
		queueID = r.opts.ProblemQueueID
	}

	w := r.word(ctx, r.opts.Templates.ProblemTicket(pin))
	problem, err := r.at.CreateTicket(ctx, NewTicket{
		CompanyID:    companyID,
		Title:        w.Title,
		Description:  w.Description,
		Priority:     t.Priority,
		Status:       t.Status,
		TicketType:   TicketTypeProblem,
		Source:       t.Source,
		QueueID:      queueID,
		IssueType:    t.IssueType,
		SubIssueType: t.SubIssueType,
		Category:     t.Category,
		WorkType:     t.WorkType,
		DueDateTime:  t.DueDateTime,
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("raising problem ticket: %w", err)
	}
	return problem, nil
}

// recurrenceFailed logs why created, a recurrence, could not be linked,
// explains it on the ticket for a person to finish, and reports the
// failure.
func (r *Receiver) recurrenceFailed(ctx context.Context, created Ticket, f RecurrenceFailure) {
	r.opts.Logger.ErrorContext(ctx, "linking recurrence to a problem ticket failed",
		"ticket", created.Number, "previous", f.Previous.Number, "error", f.Err)

	if err := r.note(ctx, created.ID, r.opts.Templates.RecurrenceFailedNote(f)); err != nil {
		r.opts.Logger.ErrorContext(ctx, "adding recurrence failure note failed", "ticket", created.Number, "error", err)
	}
	r.report(ctx, eventRecurrence{outcome: RecurrenceFailed})
}
