package receiver

import (
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

func alertName(n Notification) string {
	if name := n.GroupLabels["alertname"]; name != "" {
		return name
	}
	if name := n.CommonLabels["alertname"]; name != "" {
		return name
	}
	return "Alert group"
}

func ticketTitle(n Notification) string {
	title := alertName(n)
	if rest := formatLabels(n.GroupLabels, map[string]string{"alertname": ""}); rest != "{}" {
		title += " " + rest
	}
	return truncate(title, maxTicketTitle)
}

func ticketDescription(n Notification, firing []Alert, ambiguous []int64, previous *Ticket, continues []Ticket, overflow bool) string {
	var b strings.Builder

	if s := n.CommonAnnotations["summary"]; s != "" {
		b.WriteString(s + "\n")
	}
	if d := n.CommonAnnotations["description"]; d != "" {
		b.WriteString(d + "\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Group labels: %s\n", formatLabels(n.GroupLabels, nil))
	if link := alertmanagerLink(n); link != "" {
		fmt.Fprintf(&b, "Alertmanager: %s\n", link)
	}

	if len(ambiguous) > 0 {
		fmt.Fprintf(&b, "\nNOTE: this alert group matched the company selectors of more than one company (IDs %s) so it was raised against the catch-all company. Correct the company selectors so only one company matches.\n",
			joinIDs(ambiguous))
	}
	if previous != nil {
		fmt.Fprintf(&b, "\nPrevious ticket for this alert group: %s\n", previous.Number)
	}
	if len(continues) > 0 {
		fmt.Fprintf(&b, "\nContinues %s: alerts recorded there now belong to this alert group, usually after a change to Alertmanager's routes or group_by.\n",
			joinNumbers(continues))
	}
	if overflow {
		b.WriteString("\nNOTE: " + overflowNotice + "\n")
	}

	fmt.Fprintf(&b, "\nFiring (%d):\n", len(firing))
	writeAlerts(&b, firing, n.GroupLabels)

	return truncate(b.String(), maxTicketDescription)
}

func noteTitle(fired, resolved int) string {
	return fmt.Sprintf("%d firing, %d resolved", fired, resolved)
}

func noteDescription(n Notification, fired, resolved []Alert, overflow bool) string {
	var b strings.Builder

	if overflow {
		b.WriteString("NOTE: " + overflowNotice + "\n\n")
	}
	if len(fired) > 0 {
		fmt.Fprintf(&b, "Firing (%d):\n", len(fired))
		writeAlerts(&b, fired, n.GroupLabels)
	}
	if len(resolved) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Resolved (%d):\n", len(resolved))
		writeAlerts(&b, resolved, n.GroupLabels)
	}

	return truncate(b.String(), maxNoteDescription)
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
	writeAlerts(&b, moved, n.GroupLabels)

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

func writeAlerts(b *strings.Builder, alerts []Alert, groupLabels map[string]string) {
	for _, a := range alerts {
		fmt.Fprintf(b, "- %s %s\n", a.Labels["alertname"], formatLabels(a.Labels, withAlertname(groupLabels)))
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
