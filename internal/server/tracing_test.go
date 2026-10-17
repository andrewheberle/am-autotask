package server_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/server"
)

// spanHandler records the span context each notification is handled in.
type spanHandler struct {
	mu  sync.Mutex
	got []trace.SpanContext
}

func (h *spanHandler) Handle(ctx context.Context, _ receiver.Notification) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.got = append(h.got, trace.SpanContextFromContext(ctx))
	return nil
}

func newTracedServer(t *testing.T) (*httptest.Server, *spanHandler, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	h := &spanHandler{}
	srv := httptest.NewServer(server.New(h, server.Options{
		Token:          token,
		Metrics:        http.NotFoundHandler(),
		Logger:         slog.New(slog.DiscardHandler),
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)),
		Propagators:    propagation.TraceContext{},
	}))
	t.Cleanup(srv.Close)
	return srv, h, sr
}

func postNotification(t *testing.T, srv *httptest.Server, traceparent string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/webhook", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	if traceparent != "" {
		req.Header.Set("traceparent", traceparent)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestWebhookContinuesAlertmanagersTrace(t *testing.T) {
	srv, h, sr := newTracedServer(t)

	postNotification(t, srv, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected one server span, got %d", len(spans))
	}
	s := spans[0]
	if s.SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || s.Parent().SpanID().String() != "00f067aa0ba902b7" {
		t.Errorf("server span trace %s parent %s, want it to continue the incoming trace", s.SpanContext().TraceID(), s.Parent().SpanID())
	}
	if s.Name() != "POST /webhook" || s.SpanKind() != trace.SpanKindServer {
		t.Errorf("span = %q kind %v, want POST /webhook server span", s.Name(), s.SpanKind())
	}
	if got := h.got[0]; got.SpanID() != s.SpanContext().SpanID() {
		t.Errorf("notification handled in span %s, want the server span %s", got.SpanID(), s.SpanContext().SpanID())
	}
}

func TestWebhookStartsATraceWithoutTraceparent(t *testing.T) {
	srv, _, sr := newTracedServer(t)

	postNotification(t, srv, "")

	s := sr.Ended()[0]
	if !s.SpanContext().TraceID().IsValid() || s.Parent().IsValid() {
		t.Errorf("want a new root trace, got trace %s parent %v", s.SpanContext().TraceID(), s.Parent())
	}
}

func TestHealthAndMetricsAreNotTraced(t *testing.T) {
	srv, _, sr := newTracedServer(t)

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	if n := len(sr.Ended()); n != 0 {
		t.Errorf("expected no spans, got %d", n)
	}
}
