package receiver_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// followOptions are test options with ticket priority following severity.
func followOptions() receiver.Options {
	opts := testOptions()
	opts.SeverityOrder = []string{"critical", "warning"}
	opts.FollowSeverity = true
	opts.PriorityLabels = map[int]string{4: "Critical", 3: "Low", 2: "Medium"}
	return opts
}

// sevAlert is a HostDown alert with a severity, so warning and critical
// alerts for one instance have different fingerprints, as in Alertmanager.
func sevAlert(fingerprint, status, instance, severity string) receiver.Alert {
	return alert(fingerprint, status, map[string]string{
		"alertname": "HostDown", "tenant_id": "foo", "severity": severity, "instance": instance,
	})
}

func TestCreatedTicketPriority(t *testing.T) {
	tests := []struct {
		name        string
		order       []string
		follow      bool
		alerts      []receiver.Alert
		common      string
		want        int
		wantApplied string
	}{
		{"common severity", nil, false, []receiver.Alert{sevAlert("aaaa", "firing", "web1", "critical")}, "critical", 4, ""},
		{"mixed severities without an order", nil, false, []receiver.Alert{
			sevAlert("aaaa", "firing", "web1", "warning"), sevAlert("bbbb", "firing", "web2", "critical"),
		}, "", 3, ""},
		{"mixed severities take the highest", []string{"critical", "warning"}, false, []receiver.Alert{
			sevAlert("aaaa", "firing", "web1", "warning"), sevAlert("bbbb", "firing", "web2", "critical"),
		}, "", 4, ""},
		{"unranked severity ranks lowest", []string{"critical", "warning"}, false, []receiver.Alert{
			sevAlert("aaaa", "firing", "web1", "info"), sevAlert("bbbb", "firing", "web2", "warning"),
		}, "", 2, ""},
		{"only unranked severities", []string{"critical", "warning"}, false, []receiver.Alert{
			sevAlert("aaaa", "firing", "web1", "info"),
		}, "info", 3, ""},
		{"following severity records the applied priority", []string{"critical", "warning"}, true, []receiver.Alert{
			sevAlert("aaaa", "firing", "web1", "warning"),
		}, "warning", 2, "2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			opts := testOptions()
			opts.SeverityOrder = tt.order
			opts.FollowSeverity = tt.follow
			r := newReceiver(t, at, opts)

			n := notification(tt.alerts...)
			n.CommonLabels["severity"] = tt.common
			if tt.common == "" {
				delete(n.CommonLabels, "severity")
			}
			handle(t, r, n)

			ticket := onlyTicket(t, at)
			if ticket.Priority != tt.want || ticket.AppliedPriority != tt.wantApplied {
				t.Errorf("Priority, AppliedPriority = %d, %q; want %d, %q", ticket.Priority, ticket.AppliedPriority, tt.want, tt.wantApplied)
			}
		})
	}
}

func TestFollowingSeverityRequiresSeverityOrder(t *testing.T) {
	opts := testOptions()
	opts.FollowSeverity = true
	if _, err := receiver.New(newFakeAutotask(), fakeMatcher{}, receiver.NewMemoryLocker(), opts); err == nil {
		t.Error("New should fail without a severity order")
	}
}

// step is a notification, then optionally a technician changing the
// ticket's priority, with what the ticket should look like after it.
type step struct {
	alerts []receiver.Alert
	// setPriority, when non-zero, is applied after the notification
	setPriority int
	want        int
	wantApplied string
	// wantNote is a line the step's note must contain, or "" for no
	// priority line in any note added by the step
	wantNote string
}

func runSteps(t *testing.T, opts receiver.Options, prepare func(at *fakeAutotask, id int64), steps []step) (*fakeAutotask, *recordingObserver) {
	t.Helper()
	at := newFakeAutotask()
	obs := &recordingObserver{}
	opts.Observer = obs
	r := newReceiver(t, at, opts)

	for i, s := range steps {
		var before int
		if i > 0 {
			before = len(at.notesFor(onlyTicket(t, at).ID))
		}
		handle(t, r, notification(s.alerts...))
		if i == 0 && prepare != nil {
			prepare(at, onlyTicket(t, at).ID)
		}
		ticket := onlyTicket(t, at)

		var lines []string
		for _, n := range at.notesFor(ticket.ID)[before:] {
			lines = append(lines, n.Description)
		}
		added := strings.Join(lines, "\n")
		if s.wantNote != "" && !strings.Contains(added, s.wantNote) {
			t.Errorf("step %d: notes should contain %q:\n%s", i, s.wantNote, added)
		}
		if s.wantNote == "" && strings.Contains(added, "Priority ") {
			t.Errorf("step %d: notes should not mention priority:\n%s", i, added)
		}
		if ticket.Priority != s.want || ticket.AppliedPriority != s.wantApplied {
			t.Errorf("step %d: Priority, AppliedPriority = %d, %q; want %d, %q", i, ticket.Priority, ticket.AppliedPriority, s.want, s.wantApplied)
		}

		if s.setPriority != 0 {
			at.setPriority(ticket.ID, s.setPriority)
		}
	}
	return at, obs
}

func TestPriorityFollowsSeverity(t *testing.T) {
	warning := sevAlert("aaaa", "firing", "web1", "warning")
	critical := sevAlert("bbbb", "firing", "web1", "critical")
	criticalResolved := sevAlert("bbbb", "resolved", "web1", "critical")

	tests := []struct {
		name  string
		opts  receiver.Options
		steps []step
		want  []string
	}{
		{
			// the inhibited warning is missing while the critical fires,
			// then returns once the critical resolves
			name: "inhibited warning raised to critical and back",
			opts: followOptions(),
			steps: []step{
				{alerts: []receiver.Alert{warning}, want: 2, wantApplied: "2"},
				{alerts: []receiver.Alert{critical}, want: 4, wantApplied: "4",
					wantNote: "Priority raised from Medium (2) to Critical (4): the highest firing severity is now critical."},
				{alerts: []receiver.Alert{criticalResolved, warning}, want: 2, wantApplied: "2",
					wantNote: "Priority lowered from Critical (4) to Medium (2): the highest firing severity is now warning."},
			},
			want: []string{receiver.PriorityRaised, receiver.PriorityLowered},
		},
		{
			name: "silenced critical leaves priority",
			opts: followOptions(),
			steps: []step{
				{alerts: []receiver.Alert{warning, critical}, want: 4, wantApplied: "4"},
				{alerts: []receiver.Alert{warning}, want: 4, wantApplied: "4"},
			},
		},
		{
			name: "group resolving leaves priority",
			opts: followOptions(),
			steps: []step{
				{alerts: []receiver.Alert{warning}, want: 2, wantApplied: "2"},
				{alerts: []receiver.Alert{warning, critical}, want: 4, wantApplied: "4", wantNote: "Priority raised"},
				{alerts: []receiver.Alert{sevAlert("aaaa", "resolved", "web1", "warning"), criticalResolved}, want: 4, wantApplied: "4"},
			},
			want: []string{receiver.PriorityRaised},
		},
		{
			name: "unchanged severity leaves priority",
			opts: followOptions(),
			steps: []step{
				{alerts: []receiver.Alert{critical}, want: 4, wantApplied: "4"},
				{alerts: []receiver.Alert{critical, sevAlert("cccc", "firing", "web2", "warning")}, want: 4, wantApplied: "4"},
			},
		},
		{
			name: "not following severity",
			opts: func() receiver.Options {
				o := followOptions()
				o.FollowSeverity = false
				return o
			}(),
			steps: []step{
				{alerts: []receiver.Alert{warning}, want: 2},
				{alerts: []receiver.Alert{critical}, want: 2},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, obs := runSteps(t, tt.opts, nil, tt.steps)
			if !slices.Equal(obs.priorities, tt.want) {
				t.Errorf("priority changes reported = %v, want %v", obs.priorities, tt.want)
			}
			if obs.overrides != 0 {
				t.Errorf("overrides reported = %d, want 0", obs.overrides)
			}
		})
	}
}

func TestPriorityOverride(t *testing.T) {
	warning := sevAlert("aaaa", "firing", "web1", "warning")
	critical := sevAlert("bbbb", "firing", "web1", "critical")
	criticalResolved := sevAlert("bbbb", "resolved", "web1", "critical")

	tests := []struct {
		name          string
		prepare       func(at *fakeAutotask, id int64)
		steps         []step
		wantOverrides int
	}{
		{
			name: "operator lowers a critical ticket",
			steps: []step{
				{alerts: []receiver.Alert{critical}, want: 4, wantApplied: "4", setPriority: 3},
				{alerts: []receiver.Alert{critical, sevAlert("cccc", "firing", "web2", "critical")}, want: 3, wantApplied: "override",
					wantNote: "Priority was changed manually to Low (3), so it will no longer follow alert severity."},
				// further severity changes neither change it nor note again
				{alerts: []receiver.Alert{criticalResolved, sevAlert("cccc", "resolved", "web2", "critical"), warning}, want: 3, wantApplied: "override"},
			},
			wantOverrides: 1,
		},
		{
			name: "operator raises a warning ticket",
			steps: []step{
				{alerts: []receiver.Alert{warning}, want: 2, wantApplied: "2", setPriority: 4},
				{alerts: []receiver.Alert{critical}, want: 4, wantApplied: "override", wantNote: "changed manually to Critical (4)"},
				{alerts: []receiver.Alert{criticalResolved, warning}, want: 4, wantApplied: "override"},
			},
			wantOverrides: 1,
		},
		{
			name: "operator setting the priority back keeps the override",
			steps: []step{
				{alerts: []receiver.Alert{warning}, want: 2, wantApplied: "2", setPriority: 4},
				{alerts: []receiver.Alert{warning, critical}, want: 4, wantApplied: "override", wantNote: "changed manually", setPriority: 2},
				{alerts: []receiver.Alert{warning, critical, sevAlert("cccc", "firing", "web2", "critical")}, want: 2, wantApplied: "override"},
			},
			wantOverrides: 1,
		},
		{
			name:    "ticket opened before following is not managed",
			prepare: func(at *fakeAutotask, id int64) { at.setAppliedPriority(id, "") },
			steps: []step{
				{alerts: []receiver.Alert{warning}, want: 2, wantApplied: ""},
				{alerts: []receiver.Alert{critical}, want: 2, wantApplied: ""},
			},
		},
		{
			name:    "unreadable applied priority is not managed",
			prepare: func(at *fakeAutotask, id int64) { at.setAppliedPriority(id, "high") },
			steps: []step{
				{alerts: []receiver.Alert{warning}, want: 2, wantApplied: "high"},
				{alerts: []receiver.Alert{critical}, want: 2, wantApplied: "high"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, obs := runSteps(t, followOptions(), tt.prepare, tt.steps)
			if obs.overrides != tt.wantOverrides {
				t.Errorf("overrides reported = %d, want %d", obs.overrides, tt.wantOverrides)
			}
			if len(obs.priorities) != 0 {
				t.Errorf("priority changes reported = %v, want none", obs.priorities)
			}
		})
	}
}
