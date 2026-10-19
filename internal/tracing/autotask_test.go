package tracing_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/selector"
	"github.com/andrewheberle/am-autotask/internal/tracing"
)

func recorder() (*tracetest.SpanRecorder, *sdktrace.TracerProvider) {
	sr := tracetest.NewSpanRecorder()
	return sr, sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
}

func attr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestTransportRecordsURLWithoutQueryString(t *testing.T) {
	var gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	sr, tp := recorder()
	client := &http.Client{Transport: tracing.Transport(http.DefaultTransport, tp)}

	// a parent span, as when handling a notification
	ctx, parent := tp.Tracer("test").Start(context.Background(), "parent")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/atservicesrest/v1.0/zoneInformation?user=api%40example.com", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	parent.End()

	var span sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() != "parent" {
			span = s
		}
	}
	if span == nil {
		t.Fatal("no HTTP client span recorded")
	}
	if got, _ := attr(span, "url.full"); got.AsString() != srv.URL+"/atservicesrest/v1.0/zoneInformation" {
		t.Errorf("url.full = %q, want the URL without its query string", got.AsString())
	}
	if span.Name() != "GET zoneInformation" {
		t.Errorf("span name = %q, want %q", span.Name(), "GET zoneInformation")
	}
	if gotTraceparent != "" {
		t.Errorf("trace context should not be sent to Autotask, got traceparent %q", gotTraceparent)
	}
}

func TestTransportNamesSpansByEntity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	sr, tp := recorder()
	client := &http.Client{Transport: tracing.Transport(http.DefaultTransport, tp)}

	for _, path := range []string{"/ATServicesRest/v1.0/Tickets/query", "/ATServicesRest/v1.0/Tickets/1234/Notes", "/ATServicesRest/v1.0/Tickets/query/next?paging=x"} {
		resp, err := client.Post(srv.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	for _, s := range sr.Ended() {
		if s.Name() != "POST Tickets" {
			t.Errorf("span name = %q, want %q", s.Name(), "POST Tickets")
		}
	}
}

// fakeAutotask returns err from every operation.
type fakeAutotask struct{ err error }

func (f fakeAutotask) OpenTickets(context.Context, string) ([]receiver.Ticket, error) {
	return []receiver.Ticket{{ID: 1}, {ID: 2}}, f.err
}
func (f fakeAutotask) PreviousTicket(context.Context, string) (*receiver.Ticket, error) {
	return nil, f.err
}
func (f fakeAutotask) Ticket(context.Context, int64) (*receiver.Ticket, error) {
	return nil, f.err
}
func (f fakeAutotask) OpenAlertTickets(context.Context) ([]receiver.Ticket, error) { return nil, f.err }
func (f fakeAutotask) CreateTicket(context.Context, receiver.NewTicket) (receiver.Ticket, error) {
	return receiver.Ticket{ID: 77, Number: "T20261017.0077"}, f.err
}
func (f fakeAutotask) CreateNote(context.Context, receiver.NewNote) error { return f.err }
func (f fakeAutotask) UpdateTicket(context.Context, int64, receiver.TicketUpdate) error {
	return f.err
}

func TestAutotaskOperationsAreSpans(t *testing.T) {
	sr, tp := recorder()
	at := tracing.Autotask(fakeAutotask{}, tp)

	_, _ = at.OpenTickets(context.Background(), "am:abc")
	_, _ = at.CreateTicket(context.Background(), receiver.NewTicket{ExternalID: "am:abc"})
	_ = at.CreateNote(context.Background(), receiver.NewNote{TicketID: 77})

	spans := sr.Ended()
	want := []string{"autotask open_tickets", "autotask create_ticket", "autotask create_note"}
	if len(spans) != len(want) {
		t.Fatalf("got %d spans, want %d", len(spans), len(want))
	}
	for i, s := range spans {
		if s.Name() != want[i] {
			t.Errorf("span %d name = %q, want %q", i, s.Name(), want[i])
		}
	}
	if v, _ := attr(spans[0], "am_autotask.tickets"); v.AsInt64() != 2 {
		t.Errorf("open_tickets should record the tickets found, got %v", v.String())
	}
	if v, _ := attr(spans[1], "am_autotask.ticket.number"); v.AsString() != "T20261017.0077" {
		t.Errorf("create_ticket should record the new ticket's number, got %v", v.String())
	}
	if v, _ := attr(spans[2], "am_autotask.ticket.id"); v.AsInt64() != 77 {
		t.Errorf("create_note should record the ticket ID, got %v", v.String())
	}
}

func TestAutotaskErrorsAreRecordedByKind(t *testing.T) {
	tests := []struct {
		err  error
		kind string
	}{
		{errors.New("timeout"), "temporary"},
		{fmt.Errorf("bad request: %w", receiver.ErrPermanent), "permanent"},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			sr, tp := recorder()
			at := tracing.Autotask(fakeAutotask{err: tt.err}, tp)

			_ = at.UpdateTicket(context.Background(), 5, receiver.TicketUpdate{})

			s := sr.Ended()[0]
			if s.Status().Code != codes.Error {
				t.Errorf("status = %v, want Error", s.Status().Code)
			}
			if v, _ := attr(s, "am_autotask.failure"); v.AsString() != tt.kind {
				t.Errorf("am_autotask.failure = %q, want %q", v.AsString(), tt.kind)
			}
		})
	}
}

func TestSourceLoadsAreSpans(t *testing.T) {
	sr, tp := recorder()
	src := tracing.Source(func(context.Context) ([]selector.Company, error) {
		return []selector.Company{{ID: 1}, {ID: 2}}, nil
	}, tp)

	if _, err := src(context.Background()); err != nil {
		t.Fatal(err)
	}

	spans := sr.Ended()
	if len(spans) != 1 || spans[0].Name() != "load company selectors" {
		t.Fatalf("spans = %v, want one load company selectors span", spans)
	}
	if v, _ := attr(spans[0], "am_autotask.companies"); v.AsInt64() != 2 {
		t.Errorf("companies = %v, want 2", v.String())
	}
}
