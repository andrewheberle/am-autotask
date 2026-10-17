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
	"strings"
	"time"
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
	DueDateTime    *time.Time
	RecordedAlerts string
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
	Status         *int
	RecordedAlerts *string
	ExternalID     *string
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
	CatchAllCompanyID int64

	// SeverityLabel is the common label mapped to a priority via
	// Priorities. Default "severity".
	SeverityLabel   string
	Priorities      map[string]int
	DefaultPriority int

	Status     int
	TicketType int
	Source     int

	// optional; zero means not sent
	QueueID      int
	IssueType    int
	SubIssueType int
	Category     int
	DueIn        time.Duration

	// ResolvedStatus is applied when the whole alert group resolves; zero
	// leaves the ticket status unchanged.
	ResolvedStatus int

	NoteType    int
	NotePublish int

	// HandoverLockTimeout bounds the wait for another alert group's lock
	// when moving alerts off its orphaned ticket. Default 10 seconds.
	HandoverLockTimeout time.Duration

	// Observer, when set, is told about alerts moved off orphaned tickets.
	Observer Observer

	Now    func() time.Time
	Logger *slog.Logger
}

// Observer is told about handovers from orphaned tickets, for metrics.
type Observer interface {
	// AlertsMoved is called with the number of alerts moved off an orphaned
	// ticket.
	AlertsMoved(n int)
	// OrphanCompleted is called when an orphaned ticket is completed because
	// all of its alerts have moved.
	OrphanCompleted()
}

type noopObserver struct{}

func (noopObserver) AlertsMoved(int)  {}
func (noopObserver) OrphanCompleted() {}

// Receiver applies notifications to Autotask.
type Receiver struct {
	at      Autotask
	matcher CompanyMatcher
	locker  Locker
	opts    Options
}

// New returns a Receiver.
func New(at Autotask, matcher CompanyMatcher, locker Locker, opts Options) (*Receiver, error) {
	if opts.CatchAllCompanyID == 0 {
		return nil, errors.New("catch-all company ID is required")
	}
	if opts.DefaultPriority == 0 {
		return nil, errors.New("default priority is required")
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

	return &Receiver{at: at, matcher: matcher, locker: locker, opts: opts}, nil
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
func (r *Receiver) Handle(ctx context.Context, n Notification) error {
	externalID := ExternalID(n.GroupKey)

	unlock, err := r.locker.Lock(ctx, externalID)
	if err != nil {
		return fmt.Errorf("locking alert group: %w", err)
	}
	defer unlock()

	open, err := r.at.OpenTickets(ctx, externalID)
	if err != nil {
		return fmt.Errorf("finding current ticket: %w", err)
	}

	firing := alertsWithStatus(n.Alerts, statusFiring)
	orphans, err := r.findOrphans(ctx, externalID, firing)
	if err != nil {
		return err
	}

	var current Ticket
	if len(open) == 0 {
		created, err := r.create(ctx, n, externalID, firing, orphans)
		if err != nil || created == nil {
			return err
		}
		current = *created
	} else {
		current = open[0]
		if err := r.retireDuplicates(ctx, externalID, current, open[1:]); err != nil {
			return err
		}
		if err := r.update(ctx, n, current); err != nil {
			return err
		}
	}

	return r.handOver(ctx, n, current, orphans)
}

// retireDuplicates warns on every open ticket for the alert group other than
// the oldest, then changes their external ID so they are no longer found as
// current and are only warned once. Merging is left to a person.
func (r *Receiver) retireDuplicates(ctx context.Context, externalID string, current Ticket, duplicates []Ticket) error {
	for _, d := range duplicates {
		r.opts.Logger.Warn("alert group has more than one open ticket", "current", current.Number, "duplicate", d.Number)

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
func (r *Receiver) update(ctx context.Context, n Notification, t Ticket) error {
	groupResolved := n.Status == statusResolved
	fired, resolved, recorded, ok := diff(t.RecordedAlerts, n.Alerts, groupResolved)
	if !ok {
		return nil
	}

	if groupResolved {
		// whatever is still recorded but absent from the notification has
		// resolved along with the rest of the group
		clear(recorded)
	}
	changed := len(fired) > 0 || len(resolved) > 0 || (groupResolved && t.RecordedAlerts != "")

	var update TicketUpdate
	if changed {
		value, overflow := encodeRecorded(keys(recorded))

		title := noteTitle(len(fired), len(resolved))
		if groupResolved {
			title = "Alert group resolved: " + title
		}
		if err := r.at.CreateNote(ctx, NewNote{
			TicketID:    t.ID,
			Title:       truncate(title, maxNoteTitle),
			Description: noteDescription(n, fired, resolved, overflow),
			NoteType:    r.opts.NoteType,
			Publish:     r.opts.NotePublish,
		}); err != nil {
			return fmt.Errorf("adding note to ticket %s: %w", t.Number, err)
		}

		update.RecordedAlerts = &value
	}

	// also applied when a previous attempt noted the resolution but failed
	// to change the status
	if groupResolved && r.opts.ResolvedStatus != 0 && t.Status != r.opts.ResolvedStatus {
		update.Status = &r.opts.ResolvedStatus
	}

	if update.RecordedAlerts == nil && update.Status == nil {
		return nil
	}
	if err := r.at.UpdateTicket(ctx, t.ID, update); err != nil {
		return fmt.Errorf("updating ticket %s: %w", t.Number, err)
	}

	return nil
}

// create raises a ticket for an alert group with no current ticket,
// returning nil if nothing is firing.
func (r *Receiver) create(ctx context.Context, n Notification, externalID string, firing []Alert, orphans []orphan) (*Ticket, error) {
	if len(firing) == 0 {
		r.opts.Logger.Debug("ignoring notification with nothing firing and no current ticket", "groupKey", n.GroupKey)
		return nil, nil
	}

	previous, err := r.at.PreviousTicket(ctx, externalID)
	if err != nil {
		return nil, fmt.Errorf("finding previous ticket: %w", err)
	}

	companyID, ambiguous := r.company(ctx, n.GroupLabels)
	recorded, overflow := encodeRecorded(fingerprints(firing))

	t := NewTicket{
		CompanyID:      companyID,
		Title:          ticketTitle(n),
		Description:    ticketDescription(n, firing, ambiguous, previous, orphanTickets(orphans), overflow),
		ExternalID:     externalID,
		Priority:       r.priority(n.CommonLabels),
		Status:         r.opts.Status,
		TicketType:     r.opts.TicketType,
		Source:         r.opts.Source,
		QueueID:        r.opts.QueueID,
		IssueType:      r.opts.IssueType,
		SubIssueType:   r.opts.SubIssueType,
		Category:       r.opts.Category,
		RecordedAlerts: recorded,
	}
	if r.opts.DueIn > 0 {
		due := r.opts.Now().Add(r.opts.DueIn)
		t.DueDateTime = &due
	}

	created, err := r.at.CreateTicket(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("creating ticket: %w", err)
	}

	return &created, nil
}

// company returns the company for an alert group, and the matching company
// IDs when more than one matched.
func (r *Receiver) company(ctx context.Context, groupLabels map[string]string) (int64, []int64) {
	ids := r.matcher.Match(ctx, groupLabels)
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return r.opts.CatchAllCompanyID, nil
	default:
		r.opts.Logger.Warn("alert group matches more than one company; using catch-all company",
			"companies", ids, "groupLabels", groupLabels)
		return r.opts.CatchAllCompanyID, ids
	}
}

func (r *Receiver) priority(commonLabels map[string]string) int {
	if p, ok := r.opts.Priorities[commonLabels[r.opts.SeverityLabel]]; ok {
		return p
	}
	return r.opts.DefaultPriority
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
