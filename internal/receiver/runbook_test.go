package receiver_test

import (
	"strings"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

func withRunbook(a receiver.Alert, url string) receiver.Alert {
	a.Annotations["runbook_url"] = url
	return a
}

// withSharedRunbook sets the runbook Alertmanager reports as common to every
// alert in the notification.
func withSharedRunbook(n receiver.Notification, url string) receiver.Notification {
	n.CommonAnnotations["runbook_url"] = url
	return n
}

func TestTicketDescriptionLinksRunbooks(t *testing.T) {
	tests := []struct {
		name  string
		n     receiver.Notification
		want  []string
		count int
	}{
		{
			name: "shared by every alert",
			n: withSharedRunbook(notification(
				withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/hostdown"),
				withRunbook(hostAlert("bbbb", "firing", "web2"), "https://runbooks/hostdown"),
			), "https://runbooks/hostdown"),
			want:  []string{"Host is down\n\nRunbook: https://runbooks/hostdown\n\nGroup labels:"},
			count: 1,
		},
		{
			name: "different per alert",
			n: notification(
				withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/one"),
				withRunbook(hostAlert("bbbb", "firing", "web2"), "https://runbooks/two"),
			),
			want: []string{
				"summary of aaaa\n  Runbook: https://runbooks/one\n  Started:",
				"summary of bbbb\n  Runbook: https://runbooks/two\n  Started:",
			},
			count: 2,
		},
		{
			name: "only some alerts",
			n: notification(
				withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/hostdown"),
				hostAlert("bbbb", "firing", "web2"),
			),
			want:  []string{"summary of aaaa\n  Runbook: https://runbooks/hostdown\n"},
			count: 1,
		},
		{
			name: "surrounding whitespace trimmed",
			n: withSharedRunbook(notification(
				withRunbook(hostAlert("aaaa", "firing", "web1"), " https://runbooks/hostdown\n"),
			), " https://runbooks/hostdown\n"),
			want:  []string{"Runbook: https://runbooks/hostdown\n\nGroup labels:"},
			count: 1,
		},
		{
			name: "blank",
			n: withSharedRunbook(notification(
				withRunbook(hostAlert("aaaa", "firing", "web1"), "  "),
			), "  "),
			count: 0,
		},
		{
			name:  "none",
			n:     notification(hostAlert("aaaa", "firing", "web1")),
			count: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			r := newReceiver(t, at, testOptions())

			handle(t, r, tt.n)

			desc := onlyTicket(t, at).Description
			for _, want := range tt.want {
				if !strings.Contains(desc, want) {
					t.Errorf("Description missing %q:\n%s", want, desc)
				}
			}
			if got := strings.Count(desc, "Runbook:"); got != tt.count {
				t.Errorf("Description has %d runbook lines, want %d:\n%s", got, tt.count, desc)
			}
		})
	}
}

func TestNotesLinkRunbooksOfFiringAlertsOnly(t *testing.T) {
	first := hostAlert("aaaa", "firing", "web1")

	tests := []struct {
		name  string
		next  receiver.Notification
		want  string
		count int
	}{
		{
			name: "firing with its own runbook",
			next: notification(first, withRunbook(hostAlert("bbbb", "firing", "web2"), "https://runbooks/two")),
			want: "summary of bbbb\n  Runbook: https://runbooks/two\n",
			// the alert already recorded is not listed again
			count: 1,
		},
		{
			name: "firing with the shared runbook",
			next: withSharedRunbook(notification(
				withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/hostdown"),
				withRunbook(hostAlert("bbbb", "firing", "web2"), "https://runbooks/hostdown"),
			), "https://runbooks/hostdown"),
			count: 0,
		},
		{
			name:  "resolved",
			next:  notification(withRunbook(hostAlert("aaaa", "resolved", "web1"), "https://runbooks/hostdown")),
			count: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			r := newReceiver(t, at, testOptions())
			handle(t, r, notification(first))

			handle(t, r, tt.next)

			notes := at.notesFor(onlyTicket(t, at).ID)
			if len(notes) != 1 {
				t.Fatalf("expected one note, got %d", len(notes))
			}
			desc := notes[0].Description
			if tt.want != "" && !strings.Contains(desc, tt.want) {
				t.Errorf("note missing %q:\n%s", tt.want, desc)
			}
			if got := strings.Count(desc, "Runbook:"); got != tt.count {
				t.Errorf("note has %d runbook lines, want %d:\n%s", got, tt.count, desc)
			}
		})
	}
}

func TestMovedNoteOmitsRunbooks(t *testing.T) {
	at := newFakeAutotask()
	r := newReceiver(t, at, testOptions())
	before := notification(withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/hostdown"))
	handle(t, r, before)
	orphan := ticketFor(t, at, before)

	handle(t, r, regrouped("perth", withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/hostdown")))

	notes := at.notesFor(orphan.ID)
	if len(notes) != 1 {
		t.Fatalf("expected one note on the orphaned ticket, got %d", len(notes))
	}
	if strings.Contains(notes[0].Description, "Runbook:") {
		t.Errorf("moved note should not link runbooks:\n%s", notes[0].Description)
	}
}
