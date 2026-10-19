package leader

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Default timings, as used by Kubernetes' own controllers.
const (
	DefaultLeaseDuration = 15 * time.Second
	DefaultRenewDeadline = 10 * time.Second
	DefaultRetryPeriod   = 2 * time.Second
)

// releaseTimeout bounds releasing the Lease on shutdown.
const releaseTimeout = 5 * time.Second

// Options configures an [Elector]. Zero durations are replaced by defaults.
type Options struct {
	// Identity names this replica on the Lease. Required.
	Identity string
	// Address is this replica's host:port, published while it leads.
	// Required.
	Address string

	// LeaseDuration is how long other replicas wait, after last seeing the
	// Lease change, before taking it over.
	LeaseDuration time.Duration
	// RenewDeadline is how long the leader keeps trying to renew before
	// stepping down. It must be shorter than LeaseDuration so the leader
	// stops before anyone else can start.
	RenewDeadline time.Duration
	// RetryPeriod is the time between attempts to acquire or renew.
	RetryPeriod time.Duration

	// OnChange, when set, is called each time this replica gains or loses
	// leadership.
	OnChange func(leader bool)

	// Now returns the current time. Default [time.Now].
	Now func() time.Time
	// Logger defaults to [slog.Default].
	Logger *slog.Logger
}

// Elector campaigns for the Lease and, while holding it, lets this replica
// handle notifications. A replica that loses the Lease becomes a standby
// and campaigns again.
type Elector struct {
	client Client
	opts   Options

	mu       sync.Mutex
	leading  bool
	lead     context.Context
	stopLead context.CancelFunc
	// observed is the Lease as last read or written, and observedAt when
	// it was first seen in that state. Expiry is judged from observedAt by
	// this replica's clock, so clock skew between nodes doesn't matter.
	observed    Lease
	observedAt  time.Time
	lastContact time.Time
	failing     bool

	// in-flight holds, which stepping down waits for
	holds sync.WaitGroup
}

// New returns an Elector for the Lease client manages.
func New(client Client, opts Options) (*Elector, error) {
	if opts.Identity == "" {
		return nil, errors.New("leader election identity is required")
	}
	if opts.Address == "" {
		return nil, errors.New("leader election address is required")
	}
	if opts.LeaseDuration == 0 {
		opts.LeaseDuration = DefaultLeaseDuration
	}
	if opts.RenewDeadline == 0 {
		opts.RenewDeadline = DefaultRenewDeadline
	}
	if opts.RetryPeriod == 0 {
		opts.RetryPeriod = DefaultRetryPeriod
	}
	if opts.RenewDeadline >= opts.LeaseDuration {
		return nil, errors.New("leader election renew deadline must be shorter than the lease duration")
	}
	if opts.OnChange == nil {
		opts.OnChange = func(bool) {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Elector{client: client, opts: opts}, nil
}

// Run campaigns for the Lease until ctx is cancelled, then steps down and
// releases the Lease if held so a standby takes over straight away. Cancel
// ctx only once in-flight notifications have drained.
func (e *Elector) Run(ctx context.Context) {
	for {
		if !e.campaign(ctx) {
			return
		}
		e.renew(ctx)
		e.stepDown()
		if ctx.Err() != nil {
			e.release()
			return
		}
	}
}

// campaign tries to acquire the Lease every RetryPeriod, returning true once
// it does or false when ctx is done.
func (e *Elector) campaign(ctx context.Context) bool {
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, e.opts.RenewDeadline)
		acquired, err := e.tryAcquireOrRenew(attemptCtx)
		cancel()
		e.noteResult(ctx, err)
		if acquired {
			e.becomeLeader(ctx)
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(e.opts.RetryPeriod):
		}
	}
}

// renew keeps the Lease every RetryPeriod, returning when renewal has failed
// for RenewDeadline, another replica holds the Lease, or ctx is done.
func (e *Elector) renew(ctx context.Context) {
	lastRenew := e.opts.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.opts.RetryPeriod):
		}

		// no attempt may run past the deadline, so the leader always stops
		// before the Lease could look expired to anyone else
		attemptCtx, cancel := context.WithDeadline(ctx, lastRenew.Add(e.opts.RenewDeadline))
		renewed, err := e.tryAcquireOrRenew(attemptCtx)
		cancel()
		e.noteResult(ctx, err)

		switch {
		case renewed:
			lastRenew = e.opts.Now()
		case err == nil:
			e.opts.Logger.WarnContext(ctx, "another replica holds the leader election lease", "holder", e.holder())
			return
		case e.opts.Now().Sub(lastRenew) >= e.opts.RenewDeadline:
			e.opts.Logger.WarnContext(ctx, "could not renew the leader election lease in time", "error", err)
			return
		}
	}
}

// tryAcquireOrRenew takes the Lease if it is free, expired or already
// held by this replica, returning false without error if another replica
// holds it.
func (e *Elector) tryAcquireOrRenew(ctx context.Context) (bool, error) {
	current, err := e.client.Get(ctx)
	if errors.Is(err, ErrNotFound) {
		e.contact()
		created, err := e.client.Create(ctx, e.next(Lease{}, false))
		if err != nil {
			return false, err
		}
		e.observe(created)
		return true, nil
	}
	if err != nil {
		return false, err
	}
	e.observe(current)

	if current.Holder != "" && current.Holder != e.opts.Identity && !e.expired() {
		return false, nil
	}

	updated, err := e.client.Update(ctx, e.next(current, true))
	if err != nil {
		return false, err
	}
	e.observe(updated)
	return true, nil
}

// next returns the Lease held by this replica, renewed now.
func (e *Elector) next(prev Lease, exists bool) Lease {
	now := e.opts.Now()
	l := Lease{
		Holder:          e.opts.Identity,
		Address:         e.opts.Address,
		Duration:        e.opts.LeaseDuration,
		AcquireTime:     now,
		RenewTime:       now,
		Transitions:     prev.Transitions,
		ResourceVersion: prev.ResourceVersion,
	}
	switch {
	case !exists:
	case prev.Holder == e.opts.Identity:
		l.AcquireTime = prev.AcquireTime
	default:
		l.Transitions++
	}
	return l
}

// observe records l as the latest state of the Lease.
func (e *Elector) observe(l Lease) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.opts.Now()
	if l.ResourceVersion != e.observed.ResourceVersion || e.observedAt.IsZero() {
		e.observedAt = now
	}
	e.observed = l
	e.lastContact = now
}

// contact records a response from the API server.
func (e *Elector) contact() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastContact = e.opts.Now()
}

// expired reports whether the observed Lease has gone unrenewed for its
// duration.
func (e *Elector) expired() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.expiredLocked()
}

func (e *Elector) expiredLocked() bool {
	return !e.opts.Now().Before(e.observedAt.Add(e.observed.Duration))
}

func (e *Elector) holder() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.observed.Holder
}

// noteResult logs the first failure to reach the Lease, and recovery.
func (e *Elector) noteResult(ctx context.Context, err error) {
	if errors.Is(err, ErrConflict) {
		// another replica wrote first, which is routine
		e.contact()
		err = nil
	}

	failing := err != nil && ctx.Err() == nil
	e.mu.Lock()
	wasFailing := e.failing
	e.failing = failing
	e.mu.Unlock()

	switch {
	case failing && !wasFailing:
		e.opts.Logger.WarnContext(ctx, "leader election lease request failed", "error", err)
	case !failing && wasFailing:
		e.opts.Logger.InfoContext(ctx, "leader election lease requests are succeeding again")
	}
}

func (e *Elector) becomeLeader(ctx context.Context) {
	e.mu.Lock()
	e.leading = true
	e.lead, e.stopLead = context.WithCancel(context.Background())
	e.mu.Unlock()

	e.opts.Logger.InfoContext(ctx, "became leader", "identity", e.opts.Identity, "address", e.opts.Address)
	e.opts.OnChange(true)
}

// stepDown stops new holds, cancels those in flight and waits for them to
// return.
func (e *Elector) stepDown() {
	e.mu.Lock()
	e.leading = false
	stop := e.stopLead
	e.mu.Unlock()

	stop()
	e.holds.Wait()

	e.opts.Logger.Info("stopped leading", "identity", e.opts.Identity)
	e.opts.OnChange(false)
}

// release gives up the Lease so a standby needn't wait for it to expire.
func (e *Elector) release() {
	e.mu.Lock()
	held := e.observed
	e.mu.Unlock()
	if held.Holder != e.opts.Identity {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	now := e.opts.Now()
	released := held
	released.Holder, released.Address = "", ""
	released.Duration = time.Second
	released.AcquireTime, released.RenewTime = now, now
	if _, err := e.client.Update(ctx, released); err != nil {
		e.opts.Logger.Warn("could not release the leader election lease; a standby takes over once it expires", "error", err)
		return
	}
	e.opts.Logger.Info("released the leader election lease")
}

// Hold returns a context for handling a notification as leader, cancelled
// if leadership is lost, and a function to call when done. ok is false if
// this replica is not the leader.
func (e *Elector) Hold(ctx context.Context) (held context.Context, done func(), ok bool) {
	e.mu.Lock()
	if !e.leading {
		e.mu.Unlock()
		return nil, nil, false
	}
	// added under mu while leading, so never concurrently with stepDown's
	// Wait
	e.holds.Add(1)
	lead := e.lead
	e.mu.Unlock()

	held, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lead, cancel)
	return held, func() {
		stop()
		cancel()
		e.holds.Done()
	}, true
}

// Leading reports whether this replica is the leader.
func (e *Elector) Leading() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.leading
}

// LeaderAddress returns the current leader's address, or "" if there is no
// leader.
func (e *Elector) LeaderAddress() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.leading:
		return e.opts.Address
	case e.observed.Holder == "" || e.observed.Holder == e.opts.Identity:
		return ""
	case e.expiredLocked():
		return ""
	}
	return e.observed.Address
}

// Healthy reports whether the Kubernetes API has answered within a lease
// duration.
func (e *Elector) Healthy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.lastContact.IsZero() && e.opts.Now().Sub(e.lastContact) < e.opts.LeaseDuration
}
