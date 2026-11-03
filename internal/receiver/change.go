package receiver

import (
	"context"
	"fmt"
)

// change is what the receiver makes of one ticket: a ticket note, then an
// update, then the events to report once both are made.
type change struct {
	// note is nil when nothing is noted
	note   *Wording
	update TicketUpdate
	events []event
}

// empty reports whether the change writes nothing.
func (c change) empty() bool {
	return c.note == nil && c.update == (TicketUpdate{})
}

// commit makes a change to t, described as what in errors, such as
// "orphaned ticket". The note is written before the update, which holds the
// recorded alerts, so a failure in between repeats the note on retry rather
// than losing it (ADR-0001). Events are reported only once both succeed.
func (r *Receiver) commit(ctx context.Context, t Ticket, what string, c change) error {
	if c.note != nil {
		if err := r.note(ctx, t.ID, *c.note); err != nil {
			return fmt.Errorf("adding note to %s %s: %w", what, t.Number, err)
		}
	}
	if c.update != (TicketUpdate{}) {
		if err := r.at.UpdateTicket(ctx, t.ID, c.update); err != nil {
			return fmt.Errorf("updating %s %s: %w", what, t.Number, err)
		}
	}
	r.report(ctx, c.events...)
	return nil
}

// note adds a ticket note worded as w to the ticket with ID ticketID.
func (r *Receiver) note(ctx context.Context, ticketID int64, w Wording) error {
	return r.at.CreateNote(ctx, NewNote{
		TicketID:    ticketID,
		Title:       w.Title,
		Description: w.Description,
		NoteType:    r.opts.NoteType,
		Publish:     r.opts.NotePublish,
	})
}
