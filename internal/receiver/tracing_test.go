package receiver_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

func tracedReceiver(t *testing.T, at *fakeAutotask) (*receiver.Receiver, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	opts := testOptions()
	opts.TracerProvider = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	return newReceiver(t, at, opts), sr
}

// handleSpans returns the "handle notification" spans recorded, in order.
func handleSpans(sr *tracetest.SpanRecorder) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == "handle notification" {
			out = append(out, s)
		}
	}
	return out
}

func attrs(s sdktrace.ReadOnlySpan) map[string]attribute.Value {
	m := map[string]attribute.Value{}
	for _, kv := range s.Attributes() {
		m[string(kv.Key)] = kv.Value
	}
	return m
}

func TestHandleSpanDescribesCreatedTicket(t *testing.T) {
	at := newFakeAutotask()
	r, sr := tracedReceiver(t, at)
	n := notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "resolved", "web2"))

	handle(t, r, n)

	spans := handleSpans(sr)
	if len(spans) != 1 {
		t.Fatalf("expected one handle span, got %d", len(spans))
	}
	got := attrs(spans[0])
	ticket := onlyTicket(t, at)
	for key, want := range map[string]any{
		"am_autotask.external_id":         receiver.ExternalID(n.GroupKey),
		"am_autotask.receiver":            "autotask",
		"am_autotask.notification.status": "firing",
		"am_autotask.alerts.firing":       int64(1),
		"am_autotask.alerts.resolved":     int64(1),
		"am_autotask.outcome":             "created",
		"am_autotask.ticket.id":           ticket.ID,
		"am_autotask.ticket.number":       ticket.Number,
		"am_autotask.company.id":          int64(100),
	} {
		if v, ok := got[key]; !ok || v.AsInterface() != want {
			t.Errorf("%s = %v, want %v", key, v.AsInterface(), want)
		}
	}
	if _, ok := got["am_autotask.catchall"]; ok {
		t.Error("a matched company should not record a catch-all reason")
	}
}

func TestHandleSpanChildren(t *testing.T) {
	at := newFakeAutotask()
	r, sr := tracedReceiver(t, at)

	handle(t, r, notification(hostAlert("aaaa", "firing", "web1")))

	parent := handleSpans(sr)[0]
	children := map[string]bool{}
	for _, s := range sr.Ended() {
		if s.Parent().SpanID() == parent.SpanContext().SpanID() {
			children[s.Name()] = true
		}
	}
	for _, want := range []string{"lock alert group", "match company"} {
		if !children[want] {
			t.Errorf("handle span should have child %q, got %v", want, children)
		}
	}
}

func TestHandleSpanOutcomes(t *testing.T) {
	at := newFakeAutotask()
	r, sr := tracedReceiver(t, at)
	firing := notification(hostAlert("aaaa", "firing", "web1"))

	handle(t, r, notification(hostAlert("zzzz", "resolved", "web9")))                                    // nothing to do
	handle(t, r, firing)                                                                                 // new ticket
	handle(t, r, firing)                                                                                 // re-send
	handle(t, r, notification(hostAlert("aaaa", "firing", "web1"), hostAlert("bbbb", "firing", "web2"))) // change

	var got []string
	for _, s := range handleSpans(sr) {
		got = append(got, attrs(s)["am_autotask.outcome"].AsString())
	}
	want := []string{"ignored", "created", "unchanged", "updated"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("outcomes = %v, want %v", got, want)
	}
}

func TestHandleSpanRecordsCatchAllAndMovedAlerts(t *testing.T) {
	at := newFakeAutotask()
	r, sr := tracedReceiver(t, at)

	unmatched := notification(hostAlert("aaaa", "firing", "web1"))
	unmatched.GroupLabels["tenant_id"] = "unknown"
	handle(t, r, unmatched)
	moved := regrouped("perth", hostAlert("aaaa", "firing", "web1"))
	moved.GroupLabels["tenant_id"] = "unknown"
	handle(t, r, moved)

	spans := handleSpans(sr)
	if v := attrs(spans[0])["am_autotask.catchall"]; v.AsString() != receiver.CatchAllUnmatched {
		t.Errorf("catch-all reason = %q, want %q", v.AsString(), receiver.CatchAllUnmatched)
	}
	if v := attrs(spans[1])["am_autotask.alerts.moved"]; v.AsInt64() != 1 {
		t.Errorf("alerts moved = %v, want 1", v.AsInterface())
	}
	found := false
	for _, s := range sr.Ended() {
		if s.Name() == "hand over" && s.Parent().SpanID() == spans[1].SpanContext().SpanID() {
			found = true
		}
	}
	if !found {
		t.Error("expected a hand over span under the second handle span")
	}
}

func TestHandleSpanRecordsFailureKind(t *testing.T) {
	tests := []struct {
		err  error
		kind string
	}{
		{errors.New("timeout"), "temporary"},
		{fmt.Errorf("invalid: %w", receiver.ErrPermanent), "permanent"},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			at := newFakeAutotask()
			r, sr := tracedReceiver(t, at)
			at.failNext("CreateTicket", tt.err)

			_ = r.Handle(context.Background(), notification(hostAlert("aaaa", "firing", "web1")))

			s := handleSpans(sr)[0]
			if s.Status().Code != codes.Error {
				t.Errorf("status = %v, want Error", s.Status().Code)
			}
			if v := attrs(s)["am_autotask.failure"]; v.AsString() != tt.kind {
				t.Errorf("failure = %q, want %q", v.AsString(), tt.kind)
			}
		})
	}
}

func TestSpansRecordNoLabelsOrAnnotations(t *testing.T) {
	at := newFakeAutotask()
	r, sr := tracedReceiver(t, at)
	n := notification(hostAlert("aaaa", "firing", "secret-host-01"))
	n.CommonAnnotations["summary"] = "confidential customer outage"

	handle(t, r, n)
	handle(t, r, regrouped("perth", hostAlert("aaaa", "firing", "secret-host-01")))

	for _, s := range sr.Ended() {
		for _, kv := range s.Attributes() {
			v := kv.Value.String()
			if strings.Contains(v, "secret-host-01") || strings.Contains(v, "confidential") || strings.Contains(v, "HostDown") {
				t.Errorf("span %q attribute %s leaks alert content: %q", s.Name(), kv.Key, v)
			}
		}
	}
}
