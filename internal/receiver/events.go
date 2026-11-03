package receiver

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// event is something the receiver did that is worth reporting, once the
// writes that did it have succeeded.
type event interface {
	isEvent()
}

// eventReopened is a ticket awaiting closure reopened.
type eventReopened struct {
	ticket Ticket
}

// eventPriorityChanged is a ticket's priority following its alert group's
// severity.
type eventPriorityChanged struct {
	ticket    Ticket
	to        int
	direction string
}

// eventPriorityOverridden is a priority override first found on a ticket.
type eventPriorityOverridden struct {
	ticket Ticket
}

// eventAlertsMoved is alerts moved off an orphaned ticket onto the current
// ticket, emptying the orphan when none remain.
type eventAlertsMoved struct {
	orphan, current Ticket
	moved           int
	emptied         bool
}

// eventRecurrence is a recurrence's outcome.
type eventRecurrence struct {
	outcome string
}

func (eventReopened) isEvent()           {}
func (eventPriorityChanged) isEvent()    {}
func (eventPriorityOverridden) isEvent() {}
func (eventAlertsMoved) isEvent()        {}
func (eventRecurrence) isEvent()         {}

// report logs events, records them on the span in ctx and tells the
// Observer about them.
func (r *Receiver) report(ctx context.Context, events ...event) {
	for _, e := range events {
		switch e := e.(type) {
		case eventReopened:
			trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("am_autotask.reopened", true))
			r.opts.Logger.InfoContext(ctx, "reopened ticket awaiting closure", "ticket", e.ticket.Number)
			r.opts.Observer.Reopened()
		case eventPriorityChanged:
			r.opts.Logger.InfoContext(ctx, "ticket priority follows severity",
				"ticket", e.ticket.Number, "from", e.ticket.Priority, "to", e.to, "direction", e.direction)
			r.opts.Observer.PriorityChanged(e.direction)
		case eventPriorityOverridden:
			r.opts.Logger.InfoContext(ctx, "ticket priority overridden; no longer following severity",
				"ticket", e.ticket.Number, "priority", e.ticket.Priority)
			r.opts.Observer.PriorityOverridden()
		case eventAlertsMoved:
			r.opts.Logger.InfoContext(ctx, "moved alerts off orphaned ticket",
				"orphan", e.orphan.Number, "to", e.current.Number, "alerts", e.moved, "emptied", e.emptied)
			r.opts.Observer.AlertsMoved(e.moved)
			if e.emptied {
				r.opts.Observer.OrphanHandedOver()
			}
		case eventRecurrence:
			trace.SpanFromContext(ctx).SetAttributes(attribute.String("am_autotask.recurrence", e.outcome))
			r.opts.Observer.Recurrence(e.outcome)
		}
	}
}
