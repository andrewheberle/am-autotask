package receiver_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// recordingObserver records what the receiver reports to an Observer.
type recordingObserver struct {
	mu          sync.Mutex
	catchAll    []string
	priorities  []string
	overrides   int
	templates   []string
	recurrences []string
	reopened    int
}

func (o *recordingObserver) Reopened() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reopened++
}

func (o *recordingObserver) Recurrence(outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.recurrences = append(o.recurrences, outcome)
}

func (o *recordingObserver) AlertsMoved(int)  {}
func (o *recordingObserver) OrphanCompleted() {}

func (o *recordingObserver) PriorityChanged(direction string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.priorities = append(o.priorities, direction)
}

func (o *recordingObserver) PriorityOverridden() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.overrides++
}

func (o *recordingObserver) TemplateFailed(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.templates = append(o.templates, name)
}

func (o *recordingObserver) CatchAll(reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.catchAll = append(o.catchAll, reason)
}

func TestCatchAllTicketsAreReportedWithReason(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		want     []string
	}{
		{"matched company", "foo", nil},
		{"no company matched", "unknown", []string{receiver.CatchAllUnmatched}},
		{"several companies matched", "both", []string{receiver.CatchAllAmbiguous}},
		// the catch-all company's own selectors are a match like any other
		{"catch-all company matched", "rgi", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := newFakeAutotask()
			obs := &recordingObserver{}
			opts := testOptions()
			opts.Observer = obs
			r := newReceiver(t, at, opts)

			n := notification(hostAlert("aaaa", "firing", "web1"))
			n.GroupLabels["tenant_id"] = tt.tenantID
			handle(t, r, n)
			// later notifications for the group reuse its ticket
			handle(t, r, n)

			if !slices.Equal(obs.catchAll, tt.want) {
				t.Errorf("catch-all reports = %v, want %v", obs.catchAll, tt.want)
			}
		})
	}
}
