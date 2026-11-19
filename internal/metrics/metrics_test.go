package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/tracing"
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

func TestReopenedTicketsAreCounted(t *testing.T) {
	m := newMetrics(true, nil)

	if got := value(t, m, "am_autotask_tickets_reopened_total", ""); got != 0 {
		t.Errorf("reopened = %v before any reopen, want 0", got)
	}
	m.Reopened()
	if got := value(t, m, "am_autotask_tickets_reopened_total", ""); got != 1 {
		t.Errorf("reopened = %v, want 1", got)
	}
}

func TestHandedOverOrphansAreCounted(t *testing.T) {
	m := newMetrics(true, nil)

	if got := value(t, m, "am_autotask_orphaned_tickets_handed_over_total", ""); got != 0 {
		t.Errorf("handed over = %v before any handover, want 0", got)
	}
	m.OrphanHandedOver()
	if got := value(t, m, "am_autotask_orphaned_tickets_handed_over_total", ""); got != 1 {
		t.Errorf("handed over = %v, want 1", got)
	}
}

// fakeAutotask answers Ticket. Its other operations are not used.
type fakeAutotask struct {
	receiver.Autotask
}

func (fakeAutotask) Ticket(context.Context, int64) (*receiver.Ticket, error) {
	return &receiver.Ticket{}, nil
}

// durationExemplars returns the trace IDs of the exemplars on the duration
// histogram for operation, and fails on any exemplar without one.
func durationExemplars(t *testing.T, m *Metrics, operation string) []string {
	t.Helper()
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var traceIDs []string
	for _, f := range families {
		if f.GetName() != "am_autotask_autotask_request_duration_seconds" {
			continue
		}
		for _, s := range f.GetMetric() {
			if len(s.GetLabel()) == 0 || s.GetLabel()[0].GetValue() != operation {
				continue
			}
			for _, b := range s.GetHistogram().GetBucket() {
				if b.GetExemplar() == nil {
					continue
				}
				labels := b.GetExemplar().GetLabel()
				if len(labels) != 1 || labels[0].GetName() != exemplarTraceIDLabel {
					t.Fatalf("exemplar labels = %v, want only %s", labels, exemplarTraceIDLabel)
				}
				traceIDs = append(traceIDs, labels[0].GetValue())
			}
		}
	}
	return traceIDs
}

func TestDurationExemplars(t *testing.T) {
	tests := []struct {
		name string
		// sampler samples the operation's span; nil makes the operation
		// outside any span
		sampler      sdktrace.Sampler
		wantExemplar bool
	}{
		{name: "sampled span", sampler: sdktrace.AlwaysSample(), wantExemplar: true},
		{name: "unsampled span", sampler: sdktrace.NeverSample(), wantExemplar: false},
		{name: "no span", sampler: nil, wantExemplar: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMetrics(true, nil)
			sr := tracetest.NewSpanRecorder()
			// wrapped as serve does, so the span is live while it is observed
			at := m.Instrument(fakeAutotask{})
			if tt.sampler != nil {
				at = tracing.Autotask(at, sdktrace.NewTracerProvider(
					sdktrace.WithSampler(tt.sampler), sdktrace.WithSpanProcessor(sr),
				))
			}

			if _, err := at.Ticket(context.Background(), 1); err != nil {
				t.Fatal(err)
			}

			var want []string
			if tt.wantExemplar {
				spans := sr.Ended()
				if len(spans) != 1 {
					t.Fatalf("got %d spans, want 1", len(spans))
				}
				want = []string{spans[0].SpanContext().TraceID().String()}
			}
			if got := durationExemplars(t, m, "get_ticket"); !slices.Equal(got, want) {
				t.Errorf("exemplar trace IDs = %v, want %v", got, want)
			}
		})
	}
}

func TestHandlerExposesExemplarsWithOpenMetrics(t *testing.T) {
	traceID := trace.TraceID{0x0a, 0xf7, 0x65, 0x19, 0x16, 0xcd, 0x43, 0xdd, 0x84, 0x48, 0xeb, 0x21, 0x1c, 0x80, 0x31, 0x9c}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{0xb7, 0xad, 0x6b, 0x71, 0x69, 0x20, 0x33, 0x31},
		TraceFlags: trace.FlagsSampled,
	}))
	m := newMetrics(true, nil)
	if _, err := m.Instrument(fakeAutotask{}).Ticket(ctx, 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	exemplar := fmt.Sprintf(`# {%s="%s"}`, exemplarTraceIDLabel, traceID)
	tests := []struct {
		name         string
		accept       string
		wantType     string
		wantExemplar bool
	}{
		{name: "OpenMetrics", accept: "application/openmetrics-text; version=1.0.0", wantType: "application/openmetrics-text", wantExemplar: true},
		{name: "classic text", accept: "", wantType: "text/plain", wantExemplar: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			body := string(b)

			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, tt.wantType) {
				t.Errorf("Content-Type = %q, want %s", got, tt.wantType)
			}
			if !strings.Contains(body, `am_autotask_autotask_request_duration_seconds_bucket{operation="get_ticket",le="0.1"} 1`) {
				t.Errorf("no get_ticket duration in:\n%s", body)
			}
			if got := strings.Contains(body, exemplar); got != tt.wantExemplar {
				t.Errorf("exemplar %s present = %v, want %v, in:\n%s", exemplar, got, tt.wantExemplar, body)
			}
		})
	}
}
