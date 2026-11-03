package receiver_test

import (
	"strings"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// links are the annotations the built-in wording shows as links, with the
// prefix each is shown under.
var links = []struct {
	annotation string
	prefix     string
}{
	{annotation: "runbook_url", prefix: "Runbook:"},
	{annotation: "dashboard_url", prefix: "Dashboard:"},
}

func withAnnotation(a receiver.Alert, name, value string) receiver.Alert {
	a.Annotations[name] = value
	return a
}

// withCommonAnnotation sets an annotation Alertmanager reports as common to
// every alert in the notification.
func withCommonAnnotation(n receiver.Notification, name, value string) receiver.Notification {
	n.CommonAnnotations[name] = value
	return n
}

func withRunbook(a receiver.Alert, url string) receiver.Alert {
	return withAnnotation(a, "runbook_url", url)
}

func withSharedRunbook(n receiver.Notification, url string) receiver.Notification {
	return withCommonAnnotation(n, "runbook_url", url)
}

func withDashboard(a receiver.Alert, url string) receiver.Alert {
	return withAnnotation(a, "dashboard_url", url)
}

func withSharedDashboard(n receiver.Notification, url string) receiver.Notification {
	return withCommonAnnotation(n, "dashboard_url", url)
}

func TestTicketDescriptionShowsLinks(t *testing.T) {
	tests := []struct {
		name  string
		n     func(ann string) receiver.Notification
		want  []string // %s is replaced with the link's prefix
		count int
	}{
		{
			name: "shared by every alert",
			n: func(ann string) receiver.Notification {
				return withCommonAnnotation(notification(
					withAnnotation(hostAlert("aaaa", "firing", "web1"), ann, "https://links/hostdown"),
					withAnnotation(hostAlert("bbbb", "firing", "web2"), ann, "https://links/hostdown"),
				), ann, "https://links/hostdown")
			},
			want:  []string{"Host is down\n\n%s https://links/hostdown\n\nGroup labels:"},
			count: 1,
		},
		{
			name: "different per alert",
			n: func(ann string) receiver.Notification {
				return notification(
					withAnnotation(hostAlert("aaaa", "firing", "web1"), ann, "https://links/one"),
					withAnnotation(hostAlert("bbbb", "firing", "web2"), ann, "https://links/two"),
				)
			},
			want: []string{
				"summary of aaaa\n  %s https://links/one\n  Started:",
				"summary of bbbb\n  %s https://links/two\n  Started:",
			},
			count: 2,
		},
		{
			name: "only some alerts",
			n: func(ann string) receiver.Notification {
				return notification(
					withAnnotation(hostAlert("aaaa", "firing", "web1"), ann, "https://links/hostdown"),
					hostAlert("bbbb", "firing", "web2"),
				)
			},
			want:  []string{"summary of aaaa\n  %s https://links/hostdown\n"},
			count: 1,
		},
		{
			name: "surrounding whitespace trimmed",
			n: func(ann string) receiver.Notification {
				return withCommonAnnotation(notification(
					withAnnotation(hostAlert("aaaa", "firing", "web1"), ann, " https://links/hostdown\n"),
				), ann, " https://links/hostdown\n")
			},
			want:  []string{"%s https://links/hostdown\n\nGroup labels:"},
			count: 1,
		},
		{
			name: "blank",
			n: func(ann string) receiver.Notification {
				return withCommonAnnotation(notification(
					withAnnotation(hostAlert("aaaa", "firing", "web1"), ann, "  "),
				), ann, "  ")
			},
			count: 0,
		},
		{
			name: "none",
			n: func(string) receiver.Notification {
				return notification(hostAlert("aaaa", "firing", "web1"))
			},
			count: 0,
		},
	}
	for _, link := range links {
		for _, tt := range tests {
			t.Run(link.annotation+"/"+tt.name, func(t *testing.T) {
				at := newFakeAutotask()
				r := newReceiver(t, at, testOptions())

				handle(t, r, tt.n(link.annotation))

				desc := onlyTicket(t, at).Description
				for _, want := range tt.want {
					want = strings.ReplaceAll(want, "%s", link.prefix)
					if !strings.Contains(desc, want) {
						t.Errorf("Description missing %q:\n%s", want, desc)
					}
				}
				if got := strings.Count(desc, link.prefix); got != tt.count {
					t.Errorf("Description has %d %q lines, want %d:\n%s", got, link.prefix, tt.count, desc)
				}
			})
		}
	}
}

func TestTicketDescriptionShowsRunbookBeforeDashboard(t *testing.T) {
	tests := []struct {
		name string
		n    receiver.Notification
		want string
	}{
		{
			name: "shared",
			n: withSharedDashboard(withSharedRunbook(notification(
				withDashboard(withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/hostdown"), "https://dashboards/hosts"),
			), "https://runbooks/hostdown"), "https://dashboards/hosts"),
			want: "Runbook: https://runbooks/hostdown\nDashboard: https://dashboards/hosts\n\nGroup labels:",
		},
		{
			name: "per alert",
			n: notification(
				withDashboard(withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/hostdown"), "https://dashboards/hosts"),
				hostAlert("bbbb", "firing", "web2"),
			),
			want: "summary of aaaa\n  Runbook: https://runbooks/hostdown\n  Dashboard: https://dashboards/hosts\n  Started:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			r := newReceiver(t, at, testOptions())

			handle(t, r, tt.n)

			if desc := onlyTicket(t, at).Description; !strings.Contains(desc, tt.want) {
				t.Errorf("Description missing %q:\n%s", tt.want, desc)
			}
		})
	}
}

func TestNotesShowLinksOfFiringAlertsOnly(t *testing.T) {
	first := hostAlert("aaaa", "firing", "web1")

	tests := []struct {
		name  string
		next  func(ann string) receiver.Notification
		want  string // %s is replaced with the link's prefix
		count int
	}{
		{
			name: "firing with its own link",
			next: func(ann string) receiver.Notification {
				return notification(first, withAnnotation(hostAlert("bbbb", "firing", "web2"), ann, "https://links/two"))
			},
			want: "summary of bbbb\n  %s https://links/two\n",
			// the alert already recorded is not listed again
			count: 1,
		},
		{
			name: "firing with the shared link",
			next: func(ann string) receiver.Notification {
				return withCommonAnnotation(notification(
					withAnnotation(hostAlert("aaaa", "firing", "web1"), ann, "https://links/hostdown"),
					withAnnotation(hostAlert("bbbb", "firing", "web2"), ann, "https://links/hostdown"),
				), ann, "https://links/hostdown")
			},
			count: 0,
		},
		{
			name: "resolved",
			next: func(ann string) receiver.Notification {
				return notification(withAnnotation(hostAlert("aaaa", "resolved", "web1"), ann, "https://links/hostdown"))
			},
			count: 0,
		},
	}
	for _, link := range links {
		for _, tt := range tests {
			t.Run(link.annotation+"/"+tt.name, func(t *testing.T) {
				at := newFakeAutotask()
				r := newReceiver(t, at, testOptions())
				handle(t, r, notification(first))

				handle(t, r, tt.next(link.annotation))

				notes := at.notesFor(onlyTicket(t, at).ID)
				if len(notes) != 1 {
					t.Fatalf("expected one note, got %d", len(notes))
				}
				desc := notes[0].Description
				want := strings.ReplaceAll(tt.want, "%s", link.prefix)
				if want != "" && !strings.Contains(desc, want) {
					t.Errorf("note missing %q:\n%s", want, desc)
				}
				if got := strings.Count(desc, link.prefix); got != tt.count {
					t.Errorf("note has %d %q lines, want %d:\n%s", got, link.prefix, tt.count, desc)
				}
			})
		}
	}
}

func TestMovedNoteOmitsLinks(t *testing.T) {
	for _, link := range links {
		t.Run(link.annotation, func(t *testing.T) {
			at := newFakeAutotask()
			r := newReceiver(t, at, testOptions())
			before := notification(withAnnotation(hostAlert("aaaa", "firing", "web1"), link.annotation, "https://links/hostdown"))
			handle(t, r, before)
			orphan := ticketFor(t, at, before)

			handle(t, r, regrouped("perth", withAnnotation(hostAlert("aaaa", "firing", "web1"), link.annotation, "https://links/hostdown")))

			notes := at.notesFor(orphan.ID)
			if len(notes) != 1 {
				t.Fatalf("expected one note on the orphaned ticket, got %d", len(notes))
			}
			if strings.Contains(notes[0].Description, link.prefix) {
				t.Errorf("moved note should not show %q:\n%s", link.prefix, notes[0].Description)
			}
		})
	}
}
