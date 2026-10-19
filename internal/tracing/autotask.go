package tracing

import (
	"context"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/selector"
)

// Transport traces each HTTP request to Autotask as a client span named by
// method and entity, such as "POST Tickets". The recorded url.full has its
// query string removed, as Autotask URLs carry the API user name and search
// filters there. Trace context is not sent to Autotask.
func Transport(base http.RoundTripper, tp trace.TracerProvider) http.RoundTripper {
	return otelhttp.NewTransport(redactURL{base},
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + entity(r.URL.Path)
		}),
	)
}

// redactURL replaces the url.full otelhttp records, which includes the query
// string, before the request is sent.
type redactURL struct {
	base http.RoundTripper
}

func (t redactURL) RoundTrip(r *http.Request) (*http.Response, error) {
	u := *r.URL
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("url.full", u.String()))
	return t.base.RoundTrip(r)
}

// entity returns the Autotask entity a REST path addresses: the segment after
// the API version, e.g. "Tickets" for /ATServicesRest/v1.0/Tickets/1/Notes.
func entity(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, s := range segments {
		if strings.EqualFold(s, "v1.0") && i+1 < len(segments) {
			return segments[i+1]
		}
	}
	return segments[len(segments)-1]
}

// Autotask wraps at so each operation is a span named "autotask <operation>".
func Autotask(at receiver.Autotask, tp trace.TracerProvider) receiver.Autotask {
	return &tracedAutotask{at: at, tracer: tp.Tracer(receiver.TracerScope)}
}

type tracedAutotask struct {
	at     receiver.Autotask
	tracer trace.Tracer
}

func (t *tracedAutotask) start(ctx context.Context, operation string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, "autotask "+operation, trace.WithAttributes(attrs...))
}

func externalID(id string) attribute.KeyValue {
	return attribute.String("am_autotask.external_id", id)
}

func ticketAttrs(tk receiver.Ticket) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int64("am_autotask.ticket.id", tk.ID),
		attribute.String("am_autotask.ticket.number", tk.Number),
	}
}

func (t *tracedAutotask) OpenTickets(ctx context.Context, id string) ([]receiver.Ticket, error) {
	ctx, span := t.start(ctx, "open_tickets", externalID(id))
	tickets, err := t.at.OpenTickets(ctx, id)
	span.SetAttributes(attribute.Int("am_autotask.tickets", len(tickets)))
	receiver.EndSpan(span, err)
	return tickets, err
}

func (t *tracedAutotask) PreviousTicket(ctx context.Context, id string) (*receiver.Ticket, error) {
	ctx, span := t.start(ctx, "previous_ticket", externalID(id))
	tk, err := t.at.PreviousTicket(ctx, id)
	if tk != nil {
		span.SetAttributes(ticketAttrs(*tk)...)
	}
	receiver.EndSpan(span, err)
	return tk, err
}

func (t *tracedAutotask) Ticket(ctx context.Context, id int64) (*receiver.Ticket, error) {
	ctx, span := t.start(ctx, "get_ticket", attribute.Int64("am_autotask.ticket.id", id))
	tk, err := t.at.Ticket(ctx, id)
	if tk != nil {
		span.SetAttributes(attribute.String("am_autotask.ticket.number", tk.Number))
	}
	receiver.EndSpan(span, err)
	return tk, err
}

func (t *tracedAutotask) OpenAlertTickets(ctx context.Context) ([]receiver.Ticket, error) {
	ctx, span := t.start(ctx, "open_alert_tickets")
	tickets, err := t.at.OpenAlertTickets(ctx)
	span.SetAttributes(attribute.Int("am_autotask.tickets", len(tickets)))
	receiver.EndSpan(span, err)
	return tickets, err
}

func (t *tracedAutotask) CreateTicket(ctx context.Context, nt receiver.NewTicket) (receiver.Ticket, error) {
	ctx, span := t.start(ctx, "create_ticket", externalID(nt.ExternalID), attribute.Int64("am_autotask.company.id", nt.CompanyID))
	tk, err := t.at.CreateTicket(ctx, nt)
	if err == nil {
		span.SetAttributes(ticketAttrs(tk)...)
	}
	receiver.EndSpan(span, err)
	return tk, err
}

func (t *tracedAutotask) CreateNote(ctx context.Context, n receiver.NewNote) error {
	ctx, span := t.start(ctx, "create_note", attribute.Int64("am_autotask.ticket.id", n.TicketID))
	err := t.at.CreateNote(ctx, n)
	receiver.EndSpan(span, err)
	return err
}

func (t *tracedAutotask) UpdateTicket(ctx context.Context, id int64, u receiver.TicketUpdate) error {
	attrs := []attribute.KeyValue{attribute.Int64("am_autotask.ticket.id", id)}
	if u.Status != nil {
		attrs = append(attrs, attribute.Int("am_autotask.ticket.status", *u.Status))
	}
	if u.ProblemTicketID != nil {
		attrs = append(attrs, attribute.Int64("am_autotask.ticket.problem_id", *u.ProblemTicketID))
	}
	ctx, span := t.start(ctx, "update_ticket", attrs...)
	err := t.at.UpdateTicket(ctx, id, u)
	receiver.EndSpan(span, err)
	return err
}

// Source wraps src so each load of company selectors is a span. Periodic
// refreshes have no parent, so each is its own trace; a refresh triggered by
// an alert group matching no company is part of that notification's trace.
func Source(src selector.Source, tp trace.TracerProvider) selector.Source {
	tracer := tp.Tracer(receiver.TracerScope)
	return func(ctx context.Context) ([]selector.Company, error) {
		ctx, span := tracer.Start(ctx, "load company selectors")
		companies, err := src(ctx)
		span.SetAttributes(attribute.Int("am_autotask.companies", len(companies)))
		receiver.EndSpan(span, err)
		return companies, err
	}
}
