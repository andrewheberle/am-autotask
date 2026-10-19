package receiver

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Autotask field length limits.
const (
	maxTicketTitle       = 255
	maxTicketDescription = 8000
	maxNoteTitle         = 250
	maxNoteDescription   = 32000
)

const truncatedMarker = "… (truncated)"

// truncate cuts s to at most max characters, ending with a visible marker
// when anything was removed.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	marker := []rune(truncatedMarker)
	return string(r[:max-len(marker)]) + truncatedMarker
}

// formatLabels renders labels in Prometheus selector syntax, sorted by name,
// skipping any names in exclude.
func formatLabels(labels map[string]string, exclude map[string]string) string {
	names := make([]string, 0, len(labels))
	for k := range labels {
		if _, skip := exclude[k]; !skip {
			names = append(names, k)
		}
	}
	slices.Sort(names)

	pairs := make([]string, 0, len(names))
	for _, k := range names {
		pairs = append(pairs, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return "{" + strings.Join(pairs, ", ") + "}"
}

// render executes a ticket template and tidies the result. A failing
// template from the templates file falls back to the built-in one, and so
// does a title that renders empty.
func (r *Receiver) render(ctx context.Context, name string, data any) string {
	out, err := r.opts.Templates.render(name, data)
	if err != nil {
		r.opts.Logger.ErrorContext(ctx, "ticket template failed; using the built-in template", "template", name, "error", err)
		r.opts.Observer.TemplateFailed(name)
	}
	out = tidy(name, out)
	if out == "" && strings.HasSuffix(name, ".title") {
		r.opts.Logger.WarnContext(ctx, "ticket template rendered an empty title; using the built-in template", "template", name)
		builtin, _ := r.opts.Templates.renderBuiltin(name, data)
		out = tidy(name, builtin)
	}
	return out
}

// withNotices puts system notices ahead of a rendered description, where a
// template cannot leave them out (ADR-0005).
func withNotices(notices []string, body string) string {
	if len(notices) == 0 {
		return body
	}
	return strings.Join(notices, "\n\n") + "\n\n" + body
}

// ticketNotices are the system notices for a new ticket.
func ticketNotices(ambiguous []int64, previous *Ticket, continues []Ticket, overflow bool) []string {
	var notices []string
	if len(ambiguous) > 0 {
		notices = append(notices, fmt.Sprintf("NOTE: this alert group matched the company selectors of more than one company (IDs %s) so it was raised against the catch-all company. Correct the company selectors so only one company matches.",
			joinIDs(ambiguous)))
	}
	if previous != nil {
		notices = append(notices, "Previous ticket for this alert group: "+previous.Number)
	}
	if len(continues) > 0 {
		notices = append(notices, fmt.Sprintf("Continues %s: alerts recorded there now belong to this alert group, usually after a change to Alertmanager's routes or group_by.",
			joinNumbers(continues)))
	}
	if overflow {
		notices = append(notices, "NOTE: "+overflowNotice)
	}
	return notices
}

// noteNotices are the system notices for a change note: overflow, and the
// line describing any priority change.
func noteNotices(overflow bool, priority string) []string {
	var notices []string
	if overflow {
		notices = append(notices, "NOTE: "+overflowNotice)
	}
	if priority != "" {
		notices = append(notices, priority)
	}
	return notices
}

func movedTitle(moved int, current Ticket, emptied bool) string {
	if emptied {
		return fmt.Sprintf("All alerts moved to %s", current.Number)
	}
	if moved == 1 {
		return fmt.Sprintf("1 alert moved to %s", current.Number)
	}
	return fmt.Sprintf("%d alerts moved to %s", moved, current.Number)
}

func movedDescription(n Notification, moved []Alert, current Ticket, emptied bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "These alerts now belong to alert group %s, tracked on %s. "+
		"This usually follows a change to Alertmanager's routes or group_by.\n", formatLabels(n.GroupLabels, nil), current.Number)
	if emptied {
		fmt.Fprintf(&b, "No alerts remain on this ticket; further changes are recorded on %s.\n", current.Number)
	}
	fmt.Fprintf(&b, "\nMoved (%d):\n", len(moved))
	writeAlerts(&b, n, moved)

	return truncate(b.String(), maxNoteDescription)
}

func joinNumbers(tickets []Ticket) string {
	s := make([]string, 0, len(tickets))
	for _, t := range tickets {
		s = append(s, t.Number)
	}
	return strings.Join(s, ", ")
}

func duplicateDescription(current Ticket) string {
	return fmt.Sprintf("This ticket duplicates %s, which is the ticket now tracking this alert group. "+
		"Further changes will only be recorded on %s; merge or complete this ticket.", current.Number, current.Number)
}

func alertmanagerLink(n Notification) string {
	if n.ExternalURL == "" {
		return ""
	}
	return strings.TrimSuffix(n.ExternalURL, "/") + "/#/alerts?receiver=" + url.QueryEscape(n.Receiver)
}

// writeAlerts lists alerts for system notes.
func writeAlerts(b *strings.Builder, n Notification, alerts []Alert) {
	for _, a := range alerts {
		fmt.Fprintf(b, "- %s %s\n", a.Labels["alertname"], formatLabels(a.Labels, withAlertname(n.GroupLabels)))
		if s := a.Annotations["summary"]; s != "" {
			fmt.Fprintf(b, "  %s\n", s)
		}
		fmt.Fprintf(b, "  Started: %s\n", a.StartsAt.UTC().Format(time.RFC3339))
		if a.Status == statusResolved && !a.EndsAt.IsZero() {
			fmt.Fprintf(b, "  Ended: %s\n", a.EndsAt.UTC().Format(time.RFC3339))
		}
		if a.GeneratorURL != "" {
			fmt.Fprintf(b, "  Source: %s\n", a.GeneratorURL)
		}
	}
}

// withAlertname returns the group labels plus alertname, which is already
// shown at the start of each alert line.
func withAlertname(groupLabels map[string]string) map[string]string {
	out := make(map[string]string, len(groupLabels)+1)
	for k, v := range groupLabels {
		out[k] = v
	}
	out["alertname"] = ""
	return out
}

func joinIDs(ids []int64) string {
	s := make([]string, 0, len(ids))
	for _, id := range ids {
		s = append(s, fmt.Sprint(id))
	}
	return strings.Join(s, ", ")
}
