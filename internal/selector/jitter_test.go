package selector

import (
	"testing"
	"time"
)

func TestRefreshDelaysAreJittered(t *testing.T) {
	const interval = 15 * time.Minute
	tests := []struct {
		name  string
		delay func(time.Duration, float64) time.Duration
		r     float64
		want  time.Duration
	}{
		{name: "first, low", delay: firstDelay, r: 0, want: 0},
		{name: "first, middle", delay: firstDelay, r: 0.5, want: 7*time.Minute + 30*time.Second},
		{name: "first, high", delay: firstDelay, r: 0.999, want: 14*time.Minute + 59*time.Second + 100*time.Millisecond},
		{name: "next, low", delay: nextDelay, r: 0, want: 13*time.Minute + 30*time.Second},
		{name: "next, middle", delay: nextDelay, r: 0.5, want: interval},
		{name: "next, high", delay: nextDelay, r: 0.999, want: 16*time.Minute + 29*time.Second + 820*time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.delay(interval, tt.r)
			if diff := got - tt.want; diff < -time.Millisecond || diff > time.Millisecond {
				t.Errorf("delay = %v, want %v", got, tt.want)
			}
		})
	}
}
