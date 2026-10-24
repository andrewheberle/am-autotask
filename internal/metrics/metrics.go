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
	"github.com/andrewheberle/am-autotask/internal/server"
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
	catchAll       *prometheus.CounterVec
	priorities     *prometheus.CounterVec
	overrides      prometheus.Counter
	templates      *prometheus.CounterVec
	recurrences    *prometheus.CounterVec
	reopened       prometheus.Counter
	duration       *prometheus.HistogramVec
	transitions    prometheus.Counter
	forwarded      *prometheus.CounterVec
}

var _ receiver.Observer = (*Metrics)(nil)

// Options supplies the state reported by gauges, read at each scrape.
type Options struct {
	// SelectorsAge returns the time since company selectors were last loaded.
	SelectorsAge func() time.Duration
	// SelectorCounts returns the number of companies whose selectors were
	// valid and invalid in the last load.
	SelectorCounts func() (valid, invalid int)
	// Ready reports whether startup has completed.
	Ready func() bool
	// Leader reports whether this replica handles notifications itself.
	// Default Ready, as a single instance is always its own leader.
	Leader func() bool
}

// New registers the collectors.
func New(opts Options) *Metrics {
	if opts.Leader == nil {
		opts.Leader = opts.Ready
	}
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
		catchAll: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "catchall_tickets_total",
			Help: "Tickets raised against the catch-all company, by reason (unmatched, ambiguous).",
		}, []string{"reason"}),
		priorities: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "ticket_priority_changes_total",
			Help: "Ticket priorities changed to follow their alert group's severity, by direction (raised, lowered).",
		}, []string{"direction"}),
		overrides: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "ticket_priority_overrides_total",
			Help: "Tickets found with a manually changed priority, which then no longer follows severity.",
		}),
		templates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "template_errors_total",
			Help: "Ticket templates from the templates file that failed, so the built-in template was used instead, by template.",
		}, []string{"template"}),
		recurrences: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "recurrences_total",
			Help: "New tickets raised within the lookback period of their alert group's previous ticket being completed, by outcome (followed an open problem ticket, raised a problem ticket, failed to link).",
		}, []string{"outcome"}),
		reopened: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "tickets_reopened_total",
			Help: "Tickets awaiting closure reopened because their alert group fired again.",
		}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "autotask_request_duration_seconds",
			Help:    "Duration of Autotask API operations made while handling notifications, including paging, by operation.",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
		}, []string{"operation"}),
		transitions: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "leader_transitions_total",
			Help: "Times this replica became or stopped being the leader, with leader election.",
		}),
		forwarded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "forwarded_notifications_total",
			Help: "Notifications this standby forwarded to the leader, by result (delivered, leader_error, no_leader, unreachable).",
		}, []string{"result"}),
	}

	// start the known label values at zero so rates work from the first scrape
	for _, reason := range []string{receiver.CatchAllUnmatched, receiver.CatchAllAmbiguous} {
		m.catchAll.WithLabelValues(reason)
	}
	for _, direction := range []string{receiver.PriorityRaised, receiver.PriorityLowered} {
		m.priorities.WithLabelValues(direction)
	}
	for _, name := range receiver.TemplateNames {
		m.templates.WithLabelValues(name)
	}
	for _, outcome := range []string{receiver.RecurrenceFollowed, receiver.RecurrenceRaised, receiver.RecurrenceFailed} {
		m.recurrences.WithLabelValues(outcome)
	}
	for _, result := range server.ForwardResults {
		m.forwarded.WithLabelValues(result)
	}

	selectorCount := func(state string, pick func(valid, invalid int) int) prometheus.Collector {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: namespace, Name: "company_selectors_companies",
			Help:        "Companies with company selectors in the last load from Autotask, by state (valid, or invalid and skipped).",
			ConstLabels: prometheus.Labels{"state": state},
		}, func() float64 { return float64(pick(opts.SelectorCounts())) })
	}

	m.registry.MustRegister(
		m.notifications, m.ticketsCreated, m.notesCreated, m.autotaskErrors, m.alertsMoved, m.orphansDone,
		m.catchAll, m.priorities, m.overrides, m.templates, m.recurrences, m.reopened, m.duration, m.transitions, m.forwarded,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: namespace, Name: "company_selectors_age_seconds",
			Help: "Seconds since company selectors were last loaded from Autotask.",
		}, func() float64 { return opts.SelectorsAge().Seconds() }),
		selectorCount("valid", func(valid, _ int) int { return valid }),
		selectorCount("invalid", func(_, invalid int) int { return invalid }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: namespace, Name: "ready",
			Help: "1 once startup has completed and notifications are being handled, otherwise 0.",
		}, func() float64 {
			if opts.Ready() {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: namespace, Name: "leader",
			Help: "1 while this replica handles notifications itself: it holds the leader election lease or, without leader election, is ready. Otherwise 0.",
		}, func() float64 {
			if opts.Leader() {
				return 1
			}
			return 0
		}),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return m
}

// Handler serves the metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// CatchAll implements [receiver.Observer].
func (m *Metrics) CatchAll(reason string) {
	m.catchAll.WithLabelValues(reason).Inc()
}

// AlertsMoved implements [receiver.Observer].
func (m *Metrics) AlertsMoved(n int) {
	m.alertsMoved.Add(float64(n))
}

// OrphanCompleted implements [receiver.Observer].
func (m *Metrics) OrphanCompleted() {
	m.orphansDone.Inc()
}

// PriorityChanged implements [receiver.Observer].
func (m *Metrics) PriorityChanged(direction string) {
	m.priorities.WithLabelValues(direction).Inc()
}

// PriorityOverridden implements [receiver.Observer].
func (m *Metrics) PriorityOverridden() {
	m.overrides.Inc()
}

// TemplateFailed implements [receiver.Observer].
func (m *Metrics) TemplateFailed(template string) {
	m.templates.WithLabelValues(template).Inc()
}

// Recurrence implements [receiver.Observer].
func (m *Metrics) Recurrence(outcome string) {
	m.recurrences.WithLabelValues(outcome).Inc()
}

// Reopened implements [receiver.Observer].
func (m *Metrics) Reopened() {
	m.reopened.Inc()
}

// LeaderChanged counts this replica becoming or stopping being the leader.
func (m *Metrics) LeaderChanged(bool) {
	m.transitions.Inc()
}

// ObserveForward counts a notification forwarded to the leader by result.
func (m *Metrics) ObserveForward(result string) {
	m.forwarded.WithLabelValues(result).Inc()
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

// observe records an operation's duration and, if it failed, its error.
func (i *instrumented) observe(operation string, start time.Time, err error) error {
	i.m.duration.WithLabelValues(operation).Observe(time.Since(start).Seconds())
	if err != nil {
		i.m.observeError(operation, err)
	}
	return err
}

func (i *instrumented) OpenTickets(ctx context.Context, externalID string) ([]receiver.Ticket, error) {
	start := time.Now()
	t, err := i.at.OpenTickets(ctx, externalID)
	return t, i.observe("open_tickets", start, err)
}

func (i *instrumented) PreviousTicket(ctx context.Context, externalID string) (*receiver.Ticket, error) {
	start := time.Now()
	t, err := i.at.PreviousTicket(ctx, externalID)
	return t, i.observe("previous_ticket", start, err)
}

func (i *instrumented) Ticket(ctx context.Context, id int64) (*receiver.Ticket, error) {
	start := time.Now()
	t, err := i.at.Ticket(ctx, id)
	return t, i.observe("get_ticket", start, err)
}

func (i *instrumented) OpenAlertTickets(ctx context.Context) ([]receiver.Ticket, error) {
	start := time.Now()
	t, err := i.at.OpenAlertTickets(ctx)
	return t, i.observe("open_alert_tickets", start, err)
}

func (i *instrumented) CreateTicket(ctx context.Context, nt receiver.NewTicket) (receiver.Ticket, error) {
	start := time.Now()
	t, err := i.at.CreateTicket(ctx, nt)
	if err == nil {
		i.m.ticketsCreated.Inc()
	}
	return t, i.observe("create_ticket", start, err)
}

func (i *instrumented) CreateNote(ctx context.Context, n receiver.NewNote) error {
	start := time.Now()
	err := i.at.CreateNote(ctx, n)
	if err == nil {
		i.m.notesCreated.Inc()
	}
	return i.observe("create_note", start, err)
}

func (i *instrumented) UpdateTicket(ctx context.Context, id int64, u receiver.TicketUpdate) error {
	start := time.Now()
	err := i.at.UpdateTicket(ctx, id, u)
	return i.observe("update_ticket", start, err)
}
