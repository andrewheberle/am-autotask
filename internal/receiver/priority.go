package receiver

import (
	"context"
	"slices"
	"strconv"
)

// Directions a ticket's priority follows its alert group's severity in,
// reported to [Observer.PriorityChanged].
const (
	PriorityRaised  = "raised"
	PriorityLowered = "lowered"
)

// appliedOverride is the applied priority of a ticket with a priority
// override, whose priority is never changed again (ADR-0004).
const appliedOverride = "override"

// severity returns an alert group's severity and the priority it maps to.
func (r *Receiver) severity(n Notification) (string, int) {
	s := GroupSeverity(n, r.opts.SeverityLabel, r.opts.SeverityOrder)
	return s, r.priorityOf(s)
}

// GroupSeverity returns an alert group's severity, given by the label named
// label, default "severity". With order, ranking severities most serious
// first, this is the highest severity among the firing alerts, otherwise
// the common label.
func GroupSeverity(n Notification, label string, order []string) string {
	if label == "" {
		label = "severity"
	}
	if len(order) == 0 {
		return n.CommonLabels[label]
	}

	var highest, unranked string
	rank := len(order)
	for _, a := range alertsWithStatus(n.Alerts, statusFiring) {
		s := a.Labels[label]
		switch j := slices.Index(order, s); {
		case j >= 0 && j < rank:
			highest, rank = s, j
		case j < 0 && unranked == "":
			unranked = s
		}
	}
	if highest == "" {
		// every severity ranks lowest, so name one of them
		highest = unranked
	}
	return highest
}

func (r *Receiver) priorityOf(severity string) int {
	if p, ok := r.opts.Priorities[severity]; ok {
		return p
	}
	return r.opts.DefaultPriority
}

// priorityRank ranks a priority by the most serious severity mapping to it;
// the default priority, and any priority no severity maps to, rank lowest.
func (r *Receiver) priorityRank(p int) int {
	for i, s := range r.opts.SeverityOrder {
		if r.opts.Priorities[s] == p {
			return i
		}
	}
	return len(r.opts.SeverityOrder)
}

// priority returns a priority with its label.
func (r *Receiver) priority(p int) Priority {
	return Priority{ID: p, Label: r.opts.PriorityLabels[p]}
}

// priorityChange is how a ticket's priority follows its alert group's
// severity, made in the same note and update as the alerts that caused it.
type priorityChange struct {
	priority *int
	applied  *string
	// notice is for the change note, nil when nothing changes
	notice *PriorityNotice
}

// reprioritise works out how a ticket's priority follows its alert group's
// severity. A ticket is only managed while its applied priority is a
// priority: one opened before following was enabled, or after a priority
// override, is left alone.
func (r *Receiver) reprioritise(ctx context.Context, n Notification, t Ticket) priorityChange {
	if !r.opts.FollowSeverity || t.AppliedPriority == "" || t.AppliedPriority == appliedOverride {
		return priorityChange{}
	}
	applied, err := strconv.Atoi(t.AppliedPriority)
	if err != nil {
		r.opts.Logger.WarnContext(ctx, "ticket applied priority is not a priority ID; leaving its priority alone",
			"ticket", t.Number, "appliedPriority", t.AppliedPriority)
		return priorityChange{}
	}

	if t.Priority != applied {
		marker := appliedOverride
		return priorityChange{
			applied: &marker,
			notice:  &PriorityNotice{Override: true, To: r.priority(t.Priority)},
		}
	}

	severity, want := r.severity(n)
	if want == applied {
		return priorityChange{}
	}

	direction := PriorityLowered
	if r.priorityRank(want) < r.priorityRank(applied) {
		direction = PriorityRaised
	}
	value := strconv.Itoa(want)
	return priorityChange{
		priority: &want,
		applied:  &value,
		notice: &PriorityNotice{
			Direction: direction,
			From:      r.priority(applied),
			To:        r.priority(want),
			Severity:  severity,
		},
	}
}

// observePriority reports a priority change once it has been made.
func (r *Receiver) observePriority(ctx context.Context, t Ticket, pc priorityChange) {
	switch {
	case pc.notice == nil:
	case pc.notice.Override:
		r.opts.Logger.InfoContext(ctx, "ticket priority overridden; no longer following severity",
			"ticket", t.Number, "priority", t.Priority)
		r.opts.Observer.PriorityOverridden()
	default:
		r.opts.Logger.InfoContext(ctx, "ticket priority follows severity",
			"ticket", t.Number, "from", t.Priority, "to", *pc.priority, "direction", pc.notice.Direction)
		r.opts.Observer.PriorityChanged(pc.notice.Direction)
	}
}
