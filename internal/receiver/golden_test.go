package receiver_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata")

// TestBuiltInWordingMatchesGoldenFiles pins the built-in wording of tickets
// and notes, so a change to it shows up as a diff of testdata/*.golden.
func TestBuiltInWordingMatchesGoldenFiles(t *testing.T) {
	tests := []struct {
		name  string
		steps []receiver.Notification
		// previous, when set, adds tickets before the steps, with a lookback
		// period configured
		previous func(at *fakeAutotask)
	}{
		{
			name:  "single alert",
			steps: []receiver.Notification{notification(hostAlert("aaaa", "firing", "web1"))},
		},
		{
			name: "description and shared runbook",
			steps: []receiver.Notification{func() receiver.Notification {
				n := withSharedRunbook(notification(
					withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/hostdown"),
					withRunbook(hostAlert("bbbb", "firing", "web2"), "https://runbooks/hostdown"),
				), "https://runbooks/hostdown")
				n.CommonAnnotations["description"] = "Hosts stopped responding to probes."
				return n
			}()},
		},
		{
			name: "runbook per alert",
			steps: []receiver.Notification{notification(
				withRunbook(hostAlert("aaaa", "firing", "web1"), "https://runbooks/one"),
				hostAlert("bbbb", "firing", "web2"),
			)},
		},
		{
			name: "no alertname or annotations",
			steps: []receiver.Notification{func() receiver.Notification {
				n := notification(alert("aaaa", "firing", map[string]string{"tenant_id": "foo", "job": "node"}))
				n.GroupLabels = map[string]string{"tenant_id": "foo"}
				n.CommonLabels = map[string]string{"tenant_id": "foo", "job": "node"}
				n.CommonAnnotations = map[string]string{}
				n.ExternalURL = ""
				return n
			}()},
		},
		{
			name: "alerts fire and resolve",
			steps: []receiver.Notification{
				notification(hostAlert("aaaa", "firing", "web1")),
				notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2")),
				notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "resolved", "web2")),
				notification(hostAlert("aaaa", "resolved", "web1")),
			},
		},
		{
			name: "ambiguous company",
			steps: []receiver.Notification{func() receiver.Notification {
				n := notification(hostAlert("aaaa", "firing", "web1"))
				n.GroupLabels["tenant_id"] = "both"
				return n
			}()},
		},
		{
			name:  "recurrence raises problem ticket",
			steps: []receiver.Notification{notification(hostAlert("aaaa", "firing", "web1"))},
			previous: func(at *fakeAutotask) {
				addPrevious(at, receiver.TicketTypeIncident, 0, time.Hour)
			},
		},
		{
			name:  "recurrence follows problem ticket",
			steps: []receiver.Notification{notification(hostAlert("aaaa", "firing", "web1"))},
			previous: func(at *fakeAutotask) {
				addPrevious(at, receiver.TicketTypeIncident, addProblem(at, false), time.Hour)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			opts := testOptions()
			if tt.previous != nil {
				opts = recurrenceOptions(nil)
				tt.previous(at)
			}
			r := newReceiver(t, at, opts)
			for _, n := range tt.steps {
				handle(t, r, n)
			}

			var b strings.Builder
			for _, ticket := range at.allTickets() {
				fmt.Fprintf(&b, "=== ticket title\n%s\n=== ticket description\n%s\n", ticket.Title, ticket.Description)
				for _, note := range at.notesFor(ticket.ID) {
					fmt.Fprintf(&b, "=== note title\n%s\n=== note description\n%s\n", note.Title, note.Description)
				}
			}

			golden(t, filepath.Join("testdata", strings.ReplaceAll(tt.name, " ", "_")+".golden"), b.String())
		})
	}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run with -update to accept):\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
