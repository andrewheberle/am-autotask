package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// apiAlertGroup is one alert group from Alertmanager's /api/v2/alerts/groups.
type apiAlertGroup struct {
	Labels   map[string]string `json:"labels"`
	Receiver struct {
		Name string `json:"name"`
	} `json:"receiver"`
	Alerts []apiAlert `json:"alerts"`
}

// apiAlert is one alert within an [apiAlertGroup].
type apiAlert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
	Status       struct {
		State string `json:"state"`
	} `json:"status"`
}

// alertStateSuppressed is the state of a silenced or inhibited alert, which
// Alertmanager does not send to receivers.
const alertStateSuppressed = "suppressed"

// previewGroup is one notification to preview. A group read from
// /api/v2/alerts/groups has a heading, and is skipped when every alert in it
// is suppressed.
type previewGroup struct {
	heading      string
	notification receiver.Notification
	skipped      bool
}

// errNoAlertGroups is returned when /api/v2/alerts/groups output has nothing
// to preview.
var errNoAlertGroups = errors.New("no alert groups to preview: check the filter used to fetch them, and whether their alerts are silenced or inhibited")

// jsonKind returns the first character of the JSON value in b, such as '['
// for an array or '{' for an object.
func jsonKind(b []byte) byte {
	b = bytes.TrimLeft(b, " \t\r\n")
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

// parseNotification parses an Alertmanager webhook payload.
func parseNotification(b []byte, name string) (receiver.Notification, error) {
	if jsonKind(b) == '[' {
		return receiver.Notification{}, fmt.Errorf("--notification expects an Alertmanager webhook payload (a JSON object); %s looks like /api/v2/alerts/groups output, use --alert-groups", name)
	}
	var n receiver.Notification
	if err := json.Unmarshal(b, &n); err != nil {
		return receiver.Notification{}, fmt.Errorf("parsing notification %s: %w", name, err)
	}
	return n, nil
}

// parseAlertGroups parses the output of Alertmanager's /api/v2/alerts/groups
// into a notification per alert group, as Alertmanager would send them to a
// webhook receiver whose external URL is externalURL.
func parseAlertGroups(b []byte, name, externalURL string) ([]previewGroup, error) {
	if jsonKind(b) == '{' {
		return nil, fmt.Errorf("--alert-groups expects the JSON array from Alertmanager's /api/v2/alerts/groups; %s looks like a webhook payload, use --notification", name)
	}
	var groups []apiAlertGroup
	if err := json.Unmarshal(b, &groups); err != nil {
		return nil, fmt.Errorf("parsing alert groups %s: %w", name, err)
	}

	out := make([]previewGroup, 0, len(groups))
	skipped := 0
	for i, g := range groups {
		n := alertGroupNotification(g, externalURL)
		pg := previewGroup{
			heading:      fmt.Sprintf("alert group %d of %d: receiver %q %s", i+1, len(groups), g.Receiver.Name, formatLabelSet(g.Labels)),
			notification: n,
			skipped:      len(n.Alerts) == 0,
		}
		if pg.skipped {
			skipped++
		}
		out = append(out, pg)
	}
	if skipped == len(out) {
		return nil, errNoAlertGroups
	}
	return out, nil
}

// alertGroupNotification returns the notification Alertmanager would send
// for g: suppressed alerts are left out, every other alert is firing, and the
// common labels and annotations are those shared by every alert sent.
func alertGroupNotification(g apiAlertGroup, externalURL string) receiver.Notification {
	var alerts []receiver.Alert
	for _, a := range g.Alerts {
		if a.Status.State == alertStateSuppressed {
			continue
		}
		alerts = append(alerts, receiver.Alert{
			Status:       "firing",
			Labels:       a.Labels,
			Annotations:  a.Annotations,
			StartsAt:     a.StartsAt,
			GeneratorURL: a.GeneratorURL,
			Fingerprint:  a.Fingerprint,
		})
	}

	labels := make([]map[string]string, 0, len(alerts))
	annotations := make([]map[string]string, 0, len(alerts))
	for _, a := range alerts {
		labels = append(labels, a.Labels)
		annotations = append(annotations, a.Annotations)
	}

	return receiver.Notification{
		Version:           "4",
		GroupKey:          "{}:" + formatLabelSet(g.Labels),
		Status:            "firing",
		Receiver:          g.Receiver.Name,
		GroupLabels:       g.Labels,
		CommonLabels:      common(labels),
		CommonAnnotations: common(annotations),
		ExternalURL:       externalURL,
		Alerts:            alerts,
	}
}

// common returns the pairs found in every one of sets.
func common(sets []map[string]string) map[string]string {
	out := map[string]string{}
	if len(sets) == 0 {
		return out
	}
	for k, v := range sets[0] {
		shared := true
		for _, s := range sets[1:] {
			if w, ok := s[k]; !ok || w != v {
				shared = false
				break
			}
		}
		if shared {
			out[k] = v
		}
	}
	return out
}

// formatLabelSet formats labels in Prometheus selector syntax, sorted by
// name.
func formatLabelSet(labels map[string]string) string {
	pairs := make([]string, 0, len(labels))
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		pairs = append(pairs, k+"="+strconv.Quote(labels[k]))
	}
	return "{" + strings.Join(pairs, ", ") + "}"
}
