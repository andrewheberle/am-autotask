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

func joinNumbers(tickets []Ticket) string {
	s := make([]string, 0, len(tickets))
	for _, t := range tickets {
		s = append(s, t.Number)
	}
	return strings.Join(s, ", ")
}

func alertmanagerLink(n Notification) string {
	if n.ExternalURL == "" {
		return ""
	}
	return strings.TrimSuffix(n.ExternalURL, "/") + "/#/alerts?receiver=" + url.QueryEscape(n.Receiver)
}

// writeAlerts lists alerts for system notes, showing times in loc.
func writeAlerts(b *strings.Builder, n Notification, alerts []Alert, loc *time.Location) {
	for _, a := range alerts {
		fmt.Fprintf(b, "- %s %s\n", a.Labels["alertname"], formatLabels(a.Labels, withAlertname(n.GroupLabels)))
		if s := a.Annotations["summary"]; s != "" {
			fmt.Fprintf(b, "  %s\n", s)
		}
		fmt.Fprintf(b, "  Started: %s\n", formatTime(a.StartsAt, loc))
		if a.Status == statusResolved && !a.EndsAt.IsZero() {
			fmt.Fprintf(b, "  Ended: %s\n", formatTime(a.EndsAt, loc))
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
