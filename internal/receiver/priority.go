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
// With a severity order this is the highest severity among the firing
// alerts, otherwise the common severity label.
func (r *Receiver) severity(n Notification, firing []Alert) (string, int) {
	if len(r.opts.SeverityOrder) == 0 {
		s := n.CommonLabels[r.opts.SeverityLabel]
		return s, r.priorityOf(s)
	}

	var highest, unranked string
	rank := len(r.opts.SeverityOrder)
	for _, a := range firing {
		s := a.Labels[r.opts.SeverityLabel]
		switch j := slices.Index(r.opts.SeverityOrder, s); {
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
	return highest, r.priorityOf(highest)
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

func (r *Receiver) priorityLabel(p int) string {
	if l, ok := r.opts.PriorityLabels[p]; ok && l != "" {
		return l + " (" + strconv.Itoa(p) + ")"
	}
	return strconv.Itoa(p)
}

// priorityChange is how a ticket's priority follows its alert group's
// severity, made in the same note and update as the alerts that caused it.
type priorityChange struct {
	priority *int
	applied  *string
	// note is a line for the ticket note, empty when nothing changes
	note string
	// direction is set when the priority changes
	direction string
	// override is set when a priority override is first detected
	override bool
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
			applied:  &marker,
			note:     "Priority was changed manually to " + r.priorityLabel(t.Priority) + ", so it will no longer follow alert severity.",
			override: true,
		}
	}

	severity, want := r.severity(n, alertsWithStatus(n.Alerts, statusFiring))
	if want == applied {
		return priorityChange{}
	}

	direction := PriorityLowered
	if r.priorityRank(want) < r.priorityRank(applied) {
		direction = PriorityRaised
	}
	if severity == "" {
		severity = "unset"
	}
	value := strconv.Itoa(want)
	return priorityChange{
		priority:  &want,
		applied:   &value,
		note:      "Priority " + direction + " from " + r.priorityLabel(applied) + " to " + r.priorityLabel(want) + ": the highest firing severity is now " + severity + ".",
		direction: direction,
	}
}

// observePriority reports a priority change once it has been made.
func (r *Receiver) observePriority(ctx context.Context, t Ticket, pc priorityChange) {
	switch {
	case pc.override:
		r.opts.Logger.InfoContext(ctx, "ticket priority overridden; no longer following severity",
			"ticket", t.Number, "priority", t.Priority)
		r.opts.Observer.PriorityOverridden()
	case pc.direction != "":
		r.opts.Logger.InfoContext(ctx, "ticket priority follows severity",
			"ticket", t.Number, "from", t.Priority, "to", *pc.priority, "direction", pc.direction)
		r.opts.Observer.PriorityChanged(pc.direction)
	}
}
