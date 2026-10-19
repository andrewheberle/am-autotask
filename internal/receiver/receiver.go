// Package receiver turns Alertmanager notifications into Autotask tickets:
// one ticket per alert group, with ticket notes recording alerts firing and
// resolving.
package receiver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrPermanent marks an error that retrying the same notification cannot fix,
// such as Autotask rejecting a request as invalid.
var ErrPermanent = errors.New("permanent failure")

// Notification is an Alertmanager webhook payload (version 4).
type Notification struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

// Alert is a single alert within a [Notification].
type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

const (
	statusFiring   = "firing"
	statusResolved = "resolved"
)

// Ticket is an existing Autotask ticket for an alert group.
type Ticket struct {
	ID             int64
	Number         string
	Status         int
	ExternalID     string
	RecordedAlerts string
	Priority       int
	// AppliedPriority is the priority the receiver last set, "override"
	// after a priority override, or empty when the ticket's priority is not
	// managed.
	AppliedPriority string
}

// NewTicket is a ticket to create. Zero-valued optional IDs are omitted.
type NewTicket struct {
	CompanyID      int64
	Title          string
	Description    string
	ExternalID     string
	Priority       int
	Status         int
	TicketType     int
	Source         int
	QueueID        int
	IssueType      int
	SubIssueType   int
	Category       int
	WorkType       int
	DueDateTime    *time.Time
	RecordedAlerts string
	// AppliedPriority is empty unless the priority follows severity.
	AppliedPriority string
}

// NewNote is a ticket note to create.
type NewNote struct {
	TicketID    int64
	Title       string
	Description string
	NoteType    int
	Publish     int
}

// TicketUpdate changes the non-nil fields of a ticket in a single request.
type TicketUpdate struct {
	Status          *int
	RecordedAlerts  *string
	ExternalID      *string
	Priority        *int
	AppliedPriority *string
}

// Autotask is the subset of the Autotask API the receiver needs.
type Autotask interface {
	// OpenTickets returns the tickets with this external ID that are not
	// complete, oldest first.
	OpenTickets(ctx context.Context, externalID string) ([]Ticket, error)
	// PreviousTicket returns the most recent complete ticket with this
	// external ID, or nil if there is none.
	PreviousTicket(ctx context.Context, externalID string) (*Ticket, error)
	// OpenAlertTickets returns every ticket that is not complete and whose
	// external ID begins with "am:", across all alert groups.
	OpenAlertTickets(ctx context.Context) ([]Ticket, error)
	// CreateTicket creates a ticket, returning it with its number.
	CreateTicket(ctx context.Context, t NewTicket) (Ticket, error)
	CreateNote(ctx context.Context, n NewNote) error
	UpdateTicket(ctx context.Context, id int64, u TicketUpdate) error
}

// CompanyMatcher returns the IDs of the companies whose company selectors
// match an alert group's labels.
type CompanyMatcher interface {
	Match(ctx context.Context, groupLabels map[string]string) []int64
}

// Options configures ticket and note creation.
type Options struct {
	// CatchAllCompanyID may be 0, the Autotask instance's own company.
	CatchAllCompanyID int64

	// SeverityLabel is the common label mapped to a priority via
	// Priorities. Default "severity".
	SeverityLabel   string
	Priorities      map[string]int
	DefaultPriority int

	// SeverityOrder ranks severities, most serious first. When set, an
	// alert group's severity is the highest among its firing alerts rather
	// than its common severity label; severities not listed rank lowest.
	SeverityOrder []string
	// FollowSeverity changes a ticket's priority as its alert group's
	// severity changes, until a priority override. Requires SeverityOrder.
	FollowSeverity bool
	// PriorityLabels names priority IDs in ticket notes; IDs without a label
	// are shown as numbers.
	PriorityLabels map[int]string

	Status     int
	TicketType int
	Source     int

	// optional; zero means not sent
	QueueID      int
	IssueType    int
	SubIssueType int
	Category     int
	WorkType     int
	DueIn        time.Duration

	// ResolvedStatus is applied when the whole alert group resolves; zero
	// leaves the ticket status unchanged.
	ResolvedStatus int

	NoteType    int
	NotePublish int

	// Templates word ticket titles and descriptions and change notes.
	// Default the built-in templates.
	Templates *Templates

	// HandoverLockTimeout bounds the wait for another alert group's lock
	// when moving alerts off its orphaned ticket. Default 10 seconds.
	HandoverLockTimeout time.Duration

	// Observer, when set, is told about alerts moved off orphaned tickets.
	Observer Observer

	// TracerProvider traces handling of each notification. Default the
	// global provider.
	TracerProvider trace.TracerProvider

	Now    func() time.Time
	Logger *slog.Logger
}

// Reasons a ticket is raised against the catch-all company, reported to
// [Observer.CatchAll].
const (
	CatchAllUnmatched = "unmatched"
	CatchAllAmbiguous = "ambiguous"
)

// Observer is told about events worth counting, for metrics.
type Observer interface {
	// CatchAll is called when a ticket is raised against the catch-all
	// company, with the reason: [CatchAllUnmatched] or [CatchAllAmbiguous].
	CatchAll(reason string)
	// AlertsMoved is called with the number of alerts moved off an orphaned
	// ticket.
	AlertsMoved(n int)
	// OrphanCompleted is called when an orphaned ticket is completed because
	// all of its alerts have moved.
	OrphanCompleted()
	// PriorityChanged is called when a ticket's priority follows its alert
	// group's severity, with the direction: [PriorityRaised] or
	// [PriorityLowered].
	PriorityChanged(direction string)
	// PriorityOverridden is called once for each ticket found to have a
	// priority override.
	PriorityOverridden()
	// TemplateFailed is called with a ticket template's name when the
	// templates file's definition fails and the built-in one is used instead.
	TemplateFailed(template string)
}

type noopObserver struct{}

func (noopObserver) CatchAll(string)        {}
func (noopObserver) AlertsMoved(int)        {}
func (noopObserver) OrphanCompleted()       {}
func (noopObserver) PriorityChanged(string) {}
func (noopObserver) PriorityOverridden()    {}
func (noopObserver) TemplateFailed(string)  {}

// Receiver applies notifications to Autotask.
type Receiver struct {
	at      Autotask
	matcher CompanyMatcher
	locker  Locker
	opts    Options
	tracer  trace.Tracer
}

// New returns a Receiver.
func New(at Autotask, matcher CompanyMatcher, locker Locker, opts Options) (*Receiver, error) {
	if opts.DefaultPriority == 0 {
		return nil, errors.New("default priority is required")
	}
	if opts.FollowSeverity && len(opts.SeverityOrder) == 0 {
		return nil, errors.New("following severity requires a severity order")
	}
	if opts.SeverityLabel == "" {
		opts.SeverityLabel = "severity"
	}
	if opts.HandoverLockTimeout == 0 {
		opts.HandoverLockTimeout = 10 * time.Second
	}
	if opts.Observer == nil {
		opts.Observer = noopObserver{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.TracerProvider == nil {
		opts.TracerProvider = otel.GetTracerProvider()
	}
	if opts.Templates == nil {
		t, err := ParseTemplates("", nil)
		if err != nil {
			return nil, err
		}
		opts.Templates = t
	}

	return &Receiver{at: at, matcher: matcher, locker: locker, opts: opts, tracer: opts.TracerProvider.Tracer(TracerScope)}, nil
}

// ExternalID returns the ticket external ID for an alert group.
func ExternalID(groupKey string) string {
	sum := sha256.Sum256([]byte(groupKey))
	return "am:" + hex.EncodeToString(sum[:])[:40]
}

// duplicateExternalID is the external ID given to a retired duplicate
// ticket, keeping the group hash for traceability while no longer matching
// [ExternalID].
func duplicateExternalID(externalID string) string {
	return "am:dup:" + strings.TrimPrefix(externalID, "am:")
}

// Handle applies a notification to the alert group's current ticket,
// creating one if needed. Errors not wrapping [ErrPermanent] are worth
// retrying.
func (r *Receiver) Handle(ctx context.Context, n Notification) (err error) {
	externalID := ExternalID(n.GroupKey)
	firing := alertsWithStatus(n.Alerts, statusFiring)

	ctx, span := r.tracer.Start(ctx, "handle notification", trace.WithAttributes(
		attribute.String("am_autotask.external_id", externalID),
		attribute.String("am_autotask.receiver", n.Receiver),
		attribute.String("am_autotask.notification.status", n.Status),
		attribute.Int("am_autotask.alerts.firing", len(firing)),
		attribute.Int("am_autotask.alerts.resolved", len(alertsWithStatus(n.Alerts, statusResolved))),
	))
	defer func() { EndSpan(span, err) }()

	unlock, err := r.lock(ctx, externalID)
	if err != nil {
		return fmt.Errorf("locking alert group: %w", err)
	}
	defer unlock()

	open, err := r.at.OpenTickets(ctx, externalID)
	if err != nil {
		return fmt.Errorf("finding current ticket: %w", err)
	}

	orphans, err := r.findOrphans(ctx, externalID, firing)
	if err != nil {
		return err
	}

	var current Ticket
	outcome := OutcomeCreated
	if len(open) == 0 {
		created, err := r.create(ctx, n, externalID, firing, orphans)
		if err != nil {
			return err
		}
		if created == nil {
			span.SetAttributes(attribute.String("am_autotask.outcome", OutcomeIgnored))
			return nil
		}
		current = *created
	} else {
		current = open[0]
		if err := r.retireDuplicates(ctx, externalID, current, open[1:]); err != nil {
			return err
		}
		changed, err := r.update(ctx, n, current)
		if err != nil {
			return err
		}
		outcome = OutcomeUnchanged
		if changed {
			outcome = OutcomeUpdated
		}
	}
	span.SetAttributes(
		attribute.String("am_autotask.outcome", outcome),
		attribute.Int64("am_autotask.ticket.id", current.ID),
		attribute.String("am_autotask.ticket.number", current.Number),
	)

	return r.handOver(ctx, n, current, orphans)
}

// lock takes the alert group's lock, tracing the wait.
func (r *Receiver) lock(ctx context.Context, externalID string) (func(), error) {
	ctx, span := r.tracer.Start(ctx, "lock alert group")
	unlock, err := r.locker.Lock(ctx, externalID)
	EndSpan(span, err)
	return unlock, err
}

// retireDuplicates warns on every open ticket for the alert group other than
// the oldest, then changes their external ID so they are no longer found as
// current and are only warned once. Merging is left to a person.
func (r *Receiver) retireDuplicates(ctx context.Context, externalID string, current Ticket, duplicates []Ticket) error {
	for _, d := range duplicates {
		r.opts.Logger.WarnContext(ctx, "alert group has more than one open ticket", "current", current.Number, "duplicate", d.Number)

		if err := r.at.CreateNote(ctx, NewNote{
			TicketID:    d.ID,
			Title:       "Duplicate alert group ticket",
			Description: duplicateDescription(current),
			NoteType:    r.opts.NoteType,
			Publish:     r.opts.NotePublish,
		}); err != nil {
			return fmt.Errorf("warning on duplicate ticket %s: %w", d.Number, err)
		}

		retired := duplicateExternalID(externalID)
		if err := r.at.UpdateTicket(ctx, d.ID, TicketUpdate{ExternalID: &retired}); err != nil {
			return fmt.Errorf("retiring duplicate ticket %s: %w", d.Number, err)
		}
	}

	return nil
}

// update records the alerts that fired or resolved since the ticket last
// changed. The note is written before the recorded alerts so that a failure
// in between repeats the note on retry rather than losing it.
func (r *Receiver) update(ctx context.Context, n Notification, t Ticket) (changed bool, err error) {
	groupResolved := n.Status == statusResolved
	fired, resolved, recorded, ok := diff(t.RecordedAlerts, n.Alerts, groupResolved)
	if !ok {
		return false, nil
	}

	if groupResolved {
		// whatever is still recorded but absent from the notification has
		// resolved along with the rest of the group
		clear(recorded)
	}
	noted := len(fired) > 0 || len(resolved) > 0 || (groupResolved && t.RecordedAlerts != "")

	// priority only follows alerts firing or resolving, not alerts that are
	// silenced or inhibited and so missing from the notification
	var pc priorityChange
	if !groupResolved && (len(fired) > 0 || len(resolved) > 0) {
		pc = r.reprioritise(ctx, n, t)
	}

	var update TicketUpdate
	if noted {
		value, overflow := encodeRecorded(keys(recorded))

		severity, _ := r.severity(n, alertsWithStatus(n.Alerts, statusFiring))
		data := NoteData{
			Group:         group(n, severity),
			Fired:         alertData(fired),
			Resolved:      alertData(resolved),
			GroupResolved: groupResolved,
			TicketNumber:  t.Number,
		}
		if err := r.at.CreateNote(ctx, NewNote{
			TicketID:    t.ID,
			Title:       truncate(r.render(ctx, TemplateNoteTitle, data), maxNoteTitle),
			Description: truncate(withNotices(noteNotices(overflow, pc.note), r.render(ctx, TemplateNoteDescription, data)), maxNoteDescription),
			NoteType:    r.opts.NoteType,
			Publish:     r.opts.NotePublish,
		}); err != nil {
			return false, fmt.Errorf("adding note to ticket %s: %w", t.Number, err)
		}

		update.RecordedAlerts = &value
		update.Priority = pc.priority
		update.AppliedPriority = pc.applied
	}

	// also applied when a previous attempt noted the resolution but failed
	// to change the status
	if groupResolved && r.opts.ResolvedStatus != 0 && t.Status != r.opts.ResolvedStatus {
		update.Status = &r.opts.ResolvedStatus
	}

	if update.RecordedAlerts == nil && update.Status == nil {
		return false, nil
	}
	if err := r.at.UpdateTicket(ctx, t.ID, update); err != nil {
		return false, fmt.Errorf("updating ticket %s: %w", t.Number, err)
	}
	r.observePriority(ctx, t, pc)

	return true, nil
}

// create raises a ticket for an alert group with no current ticket,
// returning nil if nothing is firing.
func (r *Receiver) create(ctx context.Context, n Notification, externalID string, firing []Alert, orphans []orphan) (*Ticket, error) {
	if len(firing) == 0 {
		r.opts.Logger.DebugContext(ctx, "ignoring notification with nothing firing and no current ticket", "groupKey", n.GroupKey)
		return nil, nil
	}

	previous, err := r.at.PreviousTicket(ctx, externalID)
	if err != nil {
		return nil, fmt.Errorf("finding previous ticket: %w", err)
	}

	companyID, catchAll, ambiguous := r.company(ctx, n.GroupLabels)
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.Int64("am_autotask.company.id", companyID))
	if catchAll != "" {
		span.SetAttributes(attribute.String("am_autotask.catchall", catchAll))
	}
	recorded, overflow := encodeRecorded(fingerprints(firing))
	severity, priority := r.severity(n, firing)
	data := TicketData{Group: group(n, severity), Alerts: alertData(firing)}
	notices := ticketNotices(ambiguous, previous, orphanTickets(orphans), overflow)

	t := NewTicket{
		CompanyID:      companyID,
		Title:          truncate(r.render(ctx, TemplateTicketTitle, data), maxTicketTitle),
		Description:    truncate(withNotices(notices, r.render(ctx, TemplateTicketDescription, data)), maxTicketDescription),
		ExternalID:     externalID,
		Priority:       priority,
		Status:         r.opts.Status,
		TicketType:     r.opts.TicketType,
		Source:         r.opts.Source,
		QueueID:        r.opts.QueueID,
		IssueType:      r.opts.IssueType,
		SubIssueType:   r.opts.SubIssueType,
		Category:       r.opts.Category,
		WorkType:       r.opts.WorkType,
		RecordedAlerts: recorded,
	}
	if r.opts.FollowSeverity {
		t.AppliedPriority = strconv.Itoa(priority)
	}
	if r.opts.DueIn > 0 {
		due := r.opts.Now().Add(r.opts.DueIn)
		t.DueDateTime = &due
	}

	created, err := r.at.CreateTicket(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("creating ticket: %w", err)
	}

	if catchAll != "" {
		r.opts.Observer.CatchAll(catchAll)
	}

	return &created, nil
}

// company returns the company for an alert group. When that is the catch-all
// company it also returns why, and the matching company IDs when more than
// one matched.
func (r *Receiver) company(ctx context.Context, groupLabels map[string]string) (id int64, catchAll string, ambiguous []int64) {
	ctx, span := r.tracer.Start(ctx, "match company")
	defer span.End()

	ids := r.matcher.Match(ctx, groupLabels)
	span.SetAttributes(attribute.Int("am_autotask.companies.matched", len(ids)))
	switch len(ids) {
	case 1:
		return ids[0], "", nil
	case 0:
		return r.opts.CatchAllCompanyID, CatchAllUnmatched, nil
	default:
		r.opts.Logger.WarnContext(ctx, "alert group matches more than one company; using catch-all company",
			"companies", ids, "groupLabels", groupLabels)
		return r.opts.CatchAllCompanyID, CatchAllAmbiguous, ids
	}
}

func alertsWithStatus(alerts []Alert, status string) []Alert {
	var out []Alert
	for _, a := range alerts {
		if a.Status == status {
			out = append(out, a)
		}
	}
	return out
}

func fingerprints(alerts []Alert) []string {
	out := make([]string, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, a.Fingerprint)
	}
	return out
}
