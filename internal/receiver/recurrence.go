package receiver

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
// group's previous ticket being completed (ADR-0006).
type recurrence struct {
	previous Ticket
	// previousProblem is the problem ticket the previous ticket is linked
	// to, or nil when it is linked to none
	previousProblem *Ticket
	// problem is the problem ticket the new ticket is linked to, once
	// known: the previous ticket's when that is still open
	problem *Ticket
	// err is why the new ticket could not be linked
	err error
}

// follows reports whether the new ticket is linked to the previous ticket's
// open problem ticket rather than a new one.
func (rec *recurrence) follows() bool {
	return rec != nil && rec.err == nil && rec.problem != nil
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
		rec.err = fmt.Errorf("finding problem ticket %d of previous ticket %s: %w", previous.ProblemTicketID, previous.Number, err)
		return rec
	}
	rec.previousProblem = problem
	if problem != nil && problem.CompletedAt.IsZero() {
		rec.problem = problem
	}
	return rec
}

// linkPrevious reports whether the previous ticket can be linked to a new
// problem ticket: Autotask only links incidents, and a ticket already linked
// to a completed problem ticket stays there.
func (rec *recurrence) linkPrevious() bool {
	return rec.previous.Type == TicketTypeIncident && rec.previousProblem == nil
}

// linkRecurrence finishes linking created, a new ticket raised as t, to a
// problem ticket, raising one if it is not already linked. Failures are
// logged and explained on the new ticket rather than returned: the ticket
// exists, so a retry would find it as current and never link it.
func (r *Receiver) linkRecurrence(ctx context.Context, rec *recurrence, data TicketData, t NewTicket, created Ticket) {
	outcome := RecurrenceFailed
	defer func() {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("am_autotask.recurrence", outcome))
		r.opts.Observer.Recurrence(outcome)
	}()

	switch {
	case rec.err != nil:
		r.recurrenceFailed(ctx, rec, created)
		return
	case rec.problem != nil:
		outcome = RecurrenceFollowed
		return
	}

	problem, err := r.raiseProblem(ctx, rec, data, t, created)
	if err != nil {
		rec.err = err
		r.recurrenceFailed(ctx, rec, created)
		return
	}
	rec.problem = &problem

	if err := r.at.UpdateTicket(ctx, created.ID, TicketUpdate{ProblemTicketID: &problem.ID}); err != nil {
		rec.err = fmt.Errorf("linking %s to problem ticket %s: %w", created.Number, problem.Number, err)
		r.recurrenceFailed(ctx, rec, created)
		return
	}
	if rec.linkPrevious() {
		if err := r.at.UpdateTicket(ctx, rec.previous.ID, TicketUpdate{ProblemTicketID: &problem.ID}); err != nil {
			rec.err = fmt.Errorf("linking previous ticket %s to problem ticket %s: %w", rec.previous.Number, problem.Number, err)
			r.recurrenceFailed(ctx, rec, created)
			return
		}
	}

	outcome = RecurrenceRaised
	if err := r.at.CreateNote(ctx, NewNote{
		TicketID:    created.ID,
		Title:       "Linked to problem ticket " + problem.Number,
		Description: linkedNotice(problem),
		NoteType:    r.opts.NoteType,
		Publish:     r.opts.NotePublish,
	}); err != nil {
		r.opts.Logger.ErrorContext(ctx, "adding problem ticket note failed", "ticket", created.Number, "problem", problem.Number, "error", err)
	}
}

// raiseProblem creates a problem ticket for a recurrence, taking its fields
// from t, the new ticket as raised.
func (r *Receiver) raiseProblem(ctx context.Context, rec *recurrence, data TicketData, t NewTicket, created Ticket) (Ticket, error) {
	pd := ProblemData{
		TicketData:           data,
		TicketTitle:          t.Title,
		TicketNumber:         created.Number,
		PreviousTicketNumber: rec.previous.Number,
	}
	companyID := t.CompanyID
	if r.opts.ProblemCompanyID != nil {
		companyID = *r.opts.ProblemCompanyID
	}
	queueID := t.QueueID
	if r.opts.ProblemQueueID != 0 {
		queueID = r.opts.ProblemQueueID
	}

	problem, err := r.at.CreateTicket(ctx, NewTicket{
		CompanyID:    companyID,
		Title:        truncate(r.render(ctx, TemplateProblemTitle, pd), maxTicketTitle),
		Description:  truncate(withNotices(problemNotices(rec, created), r.render(ctx, TemplateProblemDescription, pd)), maxTicketDescription),
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

// recurrenceFailed logs why a recurrence could not be linked and explains it
// on the new ticket for a person to finish.
func (r *Receiver) recurrenceFailed(ctx context.Context, rec *recurrence, created Ticket) {
	r.opts.Logger.ErrorContext(ctx, "linking recurrence to a problem ticket failed",
		"ticket", created.Number, "previous", rec.previous.Number, "error", rec.err)

	if err := r.at.CreateNote(ctx, NewNote{
		TicketID:    created.ID,
		Title:       "Not linked to a problem ticket",
		Description: recurrenceFailedNotice(rec),
		NoteType:    r.opts.NoteType,
		Publish:     r.opts.NotePublish,
	}); err != nil {
		r.opts.Logger.ErrorContext(ctx, "adding recurrence failure note failed", "ticket", created.Number, "error", err)
	}
}
