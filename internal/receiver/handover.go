package receiver

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// orphan is an open ticket of another alert group that has recorded alerts
// now firing in this one. With a supported configuration an alert is in one
// alert group at a time, so it has moved: Alertmanager's routes or group_by
// changed and the ticket's own group key no longer receives notifications.
type orphan struct {
	ticket Ticket
	moved  []Alert
}

func orphanTickets(orphans []orphan) []Ticket {
	out := make([]Ticket, 0, len(orphans))
	for _, o := range orphans {
		out = append(out, o.ticket)
	}
	return out
}

// findOrphans returns the open tickets of other alert groups whose recorded
// alerts include any of the firing alerts, oldest first.
func (r *Receiver) findOrphans(ctx context.Context, externalID string, firing []Alert) ([]orphan, error) {
	if len(firing) == 0 {
		return nil, nil
	}

	tickets, err := r.at.OpenAlertTickets(ctx)
	if err != nil {
		return nil, fmt.Errorf("finding orphaned tickets: %w", err)
	}

	var orphans []orphan
	for _, t := range tickets {
		id := strings.ToLower(t.ExternalID)
		if id == strings.ToLower(externalID) || strings.HasPrefix(id, "am:dup:") || t.RecordedAlerts == recordedOverflow {
			continue
		}

		recorded := decodeRecorded(t.RecordedAlerts)
		var moved []Alert
		for _, a := range firing {
			if _, ok := recorded[a.Fingerprint]; ok {
				moved = append(moved, a)
			}
		}
		if len(moved) > 0 {
			orphans = append(orphans, orphan{ticket: t, moved: moved})
		}
	}

	slices.SortFunc(orphans, func(a, b orphan) int { return int(a.ticket.ID - b.ticket.ID) })
	return orphans, nil
}

// handOver moves alerts off each orphaned ticket onto current, recording the
// number moved on the notification's span.
func (r *Receiver) handOver(ctx context.Context, n Notification, current Ticket, orphans []orphan) (err error) {
	if len(orphans) == 0 {
		return nil
	}
	parent := trace.SpanFromContext(ctx)

	ctx, span := r.tracer.Start(ctx, "hand over", trace.WithAttributes(attribute.Int("am_autotask.orphans", len(orphans))))
	moved := 0
	defer func() {
		parent.SetAttributes(attribute.Int("am_autotask.alerts.moved", moved))
		span.SetAttributes(attribute.Int("am_autotask.alerts.moved", moved))
		EndSpan(span, err)
	}()

	for _, o := range orphans {
		m, err := r.moveFrom(ctx, n, current, o)
		moved += m
		if err != nil {
			return err
		}
	}
	return nil
}

// moveFrom notes the moved alerts on an orphaned ticket and stops recording
// them there, completing the ticket once nothing is left. It holds the
// orphaned ticket's group lock, re-reading the ticket under it, so moves from
// a split group cannot overwrite each other.
func (r *Receiver) moveFrom(ctx context.Context, n Notification, current Ticket, o orphan) (int, error) {
	lockCtx, cancel := context.WithTimeout(ctx, r.opts.HandoverLockTimeout)
	unlock, err := r.locker.Lock(lockCtx, o.ticket.ExternalID)
	cancel()
	if err != nil {
		return 0, fmt.Errorf("locking orphaned ticket %s: %w", o.ticket.Number, err)
	}
	defer unlock()

	open, err := r.at.OpenTickets(ctx, o.ticket.ExternalID)
	if err != nil {
		return 0, fmt.Errorf("re-reading orphaned ticket %s: %w", o.ticket.Number, err)
	}
	i := slices.IndexFunc(open, func(t Ticket) bool { return t.ID == o.ticket.ID })
	if i < 0 {
		// completed meanwhile
		return 0, nil
	}
	t := open[i]

	recorded := decodeRecorded(t.RecordedAlerts)
	var moved []Alert
	for _, a := range o.moved {
		if _, ok := recorded[a.Fingerprint]; ok {
			moved = append(moved, a)
			delete(recorded, a.Fingerprint)
		}
	}
	if len(moved) == 0 {
		return 0, nil
	}

	value, _ := encodeRecorded(keys(recorded))
	emptied := value == ""

	if err := r.at.CreateNote(ctx, NewNote{
		TicketID:    t.ID,
		Title:       truncate(movedTitle(len(moved), current, emptied), maxNoteTitle),
		Description: movedDescription(n, moved, current, emptied, r.opts.Templates.loc),
		NoteType:    r.opts.NoteType,
		Publish:     r.opts.NotePublish,
	}); err != nil {
		return 0, fmt.Errorf("adding note to orphaned ticket %s: %w", t.Number, err)
	}

	update := TicketUpdate{RecordedAlerts: &value}
	if emptied && r.opts.ResolvedStatus != 0 {
		update.Status = &r.opts.ResolvedStatus
	}
	if err := r.at.UpdateTicket(ctx, t.ID, update); err != nil {
		return 0, fmt.Errorf("updating orphaned ticket %s: %w", t.Number, err)
	}

	r.opts.Logger.InfoContext(ctx, "moved alerts off orphaned ticket",
		"orphan", t.Number, "to", current.Number, "alerts", len(moved), "emptied", emptied)
	r.opts.Observer.AlertsMoved(len(moved))
	if emptied {
		r.opts.Observer.OrphanHandedOver()
	}

	return len(moved), nil
}
