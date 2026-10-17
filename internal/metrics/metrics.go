// Package metrics exposes Prometheus metrics for the receiver.
package metrics

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/andrewheberle/am-autotask/internal/autotask"
	"github.com/andrewheberle/am-autotask/internal/receiver"
)

const namespace = "am_autotask"

// Metrics holds the receiver's collectors in their own registry.
type Metrics struct {
	registry       *prometheus.Registry
	notifications  *prometheus.CounterVec
	ticketsCreated prometheus.Counter
	notesCreated   prometheus.Counter
	autotaskErrors *prometheus.CounterVec
	alertsMoved    prometheus.Counter
	orphansDone    prometheus.Counter
}

var _ receiver.Observer = (*Metrics)(nil)

// New registers the collectors. selectorsAge reports the time since company
// selectors were last loaded.
func New(selectorsAge func() time.Duration) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		notifications: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "notifications_total",
			Help: "Webhook notifications received, by result.",
		}, []string{"result"}),
		ticketsCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "tickets_created_total",
			Help: "Autotask tickets created.",
		}),
		notesCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "notes_created_total",
			Help: "Autotask ticket notes created.",
		}),
		autotaskErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "autotask_errors_total",
			Help: "Failed Autotask API operations, by operation and kind (permanent, temporary, rate_limited).",
		}, []string{"operation", "kind"}),
		alertsMoved: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "alerts_moved_total",
			Help: "Alerts moved off orphaned tickets onto their alert group's current ticket.",
		}),
		orphansDone: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "orphaned_tickets_completed_total",
			Help: "Orphaned tickets completed because all of their alerts moved.",
		}),
	}

	m.registry.MustRegister(
		m.notifications, m.ticketsCreated, m.notesCreated, m.autotaskErrors, m.alertsMoved, m.orphansDone,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: namespace, Name: "company_selectors_age_seconds",
			Help: "Seconds since company selectors were last loaded from Autotask.",
		}, func() float64 { return selectorsAge().Seconds() }),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return m
}

// Handler serves the metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// AlertsMoved implements [receiver.Observer].
func (m *Metrics) AlertsMoved(n int) {
	m.alertsMoved.Add(float64(n))
}

// OrphanCompleted implements [receiver.Observer].
func (m *Metrics) OrphanCompleted() {
	m.orphansDone.Inc()
}

// ObserveNotification counts a webhook request by result.
func (m *Metrics) ObserveNotification(result string) {
	m.notifications.WithLabelValues(result).Inc()
}

func (m *Metrics) observeError(operation string, err error) {
	kind := "temporary"
	switch {
	case autotask.IsRateLimited(err):
		kind = "rate_limited"
	case errors.Is(err, receiver.ErrPermanent):
		kind = "permanent"
	}
	m.autotaskErrors.WithLabelValues(operation, kind).Inc()
}

// Instrument wraps at so its operations are counted.
func (m *Metrics) Instrument(at receiver.Autotask) receiver.Autotask {
	return &instrumented{at: at, m: m}
}

type instrumented struct {
	at receiver.Autotask
	m  *Metrics
}

func (i *instrumented) observe(operation string, err error) error {
	if err != nil {
		i.m.observeError(operation, err)
	}
	return err
}

func (i *instrumented) OpenTickets(ctx context.Context, externalID string) ([]receiver.Ticket, error) {
	t, err := i.at.OpenTickets(ctx, externalID)
	return t, i.observe("open_tickets", err)
}

func (i *instrumented) PreviousTicket(ctx context.Context, externalID string) (*receiver.Ticket, error) {
	t, err := i.at.PreviousTicket(ctx, externalID)
	return t, i.observe("previous_ticket", err)
}

func (i *instrumented) OpenAlertTickets(ctx context.Context) ([]receiver.Ticket, error) {
	t, err := i.at.OpenAlertTickets(ctx)
	return t, i.observe("open_alert_tickets", err)
}

func (i *instrumented) CreateTicket(ctx context.Context, nt receiver.NewTicket) (receiver.Ticket, error) {
	t, err := i.at.CreateTicket(ctx, nt)
	if err == nil {
		i.m.ticketsCreated.Inc()
	}
	return t, i.observe("create_ticket", err)
}

func (i *instrumented) CreateNote(ctx context.Context, n receiver.NewNote) error {
	err := i.at.CreateNote(ctx, n)
	if err == nil {
		i.m.notesCreated.Inc()
	}
	return i.observe("create_note", err)
}

func (i *instrumented) UpdateTicket(ctx context.Context, id int64, u receiver.TicketUpdate) error {
	return i.observe("update_ticket", i.at.UpdateTicket(ctx, id, u))
}
