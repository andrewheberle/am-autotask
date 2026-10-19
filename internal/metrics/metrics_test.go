package metrics

import (
	"testing"
	"time"
)

// value returns the value of the series of the named family with the given
// label value, if any.
func value(t *testing.T, m *Metrics, name, label string) float64 {
	t.Helper()
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, s := range f.GetMetric() {
			if label != "" && (len(s.GetLabel()) == 0 || s.GetLabel()[0].GetValue() != label) {
				continue
			}
			switch {
			case s.GetGauge() != nil:
				return s.GetGauge().GetValue()
			case s.GetCounter() != nil:
				return s.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("no %s{%s}", name, label)
	return 0
}

func newMetrics(ready bool, leader func() bool) *Metrics {
	return New(Options{
		SelectorsAge:   func() time.Duration { return 0 },
		SelectorCounts: func() (int, int) { return 0, 0 },
		Ready:          func() bool { return ready },
		Leader:         leader,
	})
}

func TestLeaderGauge(t *testing.T) {
	tests := []struct {
		name   string
		ready  bool
		leader func() bool
		want   float64
	}{
		{name: "single instance once ready", ready: true, want: 1},
		{name: "single instance while starting", ready: false, want: 0},
		{name: "standby", ready: true, leader: func() bool { return false }, want: 0},
		{name: "leader", ready: true, leader: func() bool { return true }, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := value(t, newMetrics(tt.ready, tt.leader), "am_autotask_leader", ""); got != tt.want {
				t.Errorf("am_autotask_leader = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLeaderChangesAndForwardsAreCounted(t *testing.T) {
	m := newMetrics(true, nil)

	if got := value(t, m, "am_autotask_forwarded_notifications_total", "no_leader"); got != 0 {
		t.Errorf("forwarded{no_leader} = %v before any forward, want 0", got)
	}
	m.LeaderChanged(true)
	m.LeaderChanged(false)
	m.ObserveForward("delivered")
	if got := value(t, m, "am_autotask_leader_transitions_total", ""); got != 2 {
		t.Errorf("transitions = %v, want 2", got)
	}
	if got := value(t, m, "am_autotask_forwarded_notifications_total", "delivered"); got != 1 {
		t.Errorf("forwarded{delivered} = %v, want 1", got)
	}
}
