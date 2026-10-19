package leader

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeClient is an in-memory Lease with Kubernetes' optimistic concurrency.
type fakeClient struct {
	mu      sync.Mutex
	lease   *Lease
	version int
	err     error
}

func (f *fakeClient) Get(context.Context) (Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.err != nil:
		return Lease{}, f.err
	case f.lease == nil:
		return Lease{}, ErrNotFound
	}
	return *f.lease, nil
}

func (f *fakeClient) Create(_ context.Context, l Lease) (Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.err != nil:
		return Lease{}, f.err
	case f.lease != nil:
		return Lease{}, ErrConflict
	}
	return f.store(l), nil
}

func (f *fakeClient) Update(_ context.Context, l Lease) (Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.err != nil:
		return Lease{}, f.err
	case f.lease == nil:
		return Lease{}, ErrNotFound
	case f.lease.ResourceVersion != l.ResourceVersion:
		return Lease{}, ErrConflict
	}
	return f.store(l), nil
}

func (f *fakeClient) store(l Lease) Lease {
	f.version++
	l.ResourceVersion = strconv.Itoa(f.version)
	f.lease = &l
	return l
}

func (f *fakeClient) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeClient) current() Lease {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lease == nil {
		return Lease{}
	}
	return *f.lease
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var start = time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)

func newElector(t *testing.T, client Client, identity string, opts Options) *Elector {
	t.Helper()
	opts.Identity = identity
	opts.Address = identity + ":8080"
	opts.Logger = slog.New(slog.DiscardHandler)
	e, err := New(client, opts)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestTryAcquireOrRenew(t *testing.T) {
	other := Lease{Holder: "other", Address: "other:8080", Duration: 15 * time.Second, AcquireTime: start, RenewTime: start, Transitions: 2}

	tests := []struct {
		name string
		// existing Lease, nil for none
		existing *Lease
		// time passed since the Lease was first observed
		wait time.Duration

		wantAcquired    bool
		wantHolder      string
		wantTransitions int32
		wantAcquireTime time.Time
	}{
		{name: "creates a missing lease", wantAcquired: true, wantHolder: "me", wantTransitions: 0},
		{name: "leaves a lease held by another", existing: &other, wait: 14 * time.Second, wantHolder: "other", wantTransitions: 2, wantAcquireTime: start},
		{name: "takes over an expired lease", existing: &other, wait: 15 * time.Second, wantAcquired: true, wantHolder: "me", wantTransitions: 3},
		{
			name:         "takes a released lease straight away",
			existing:     &Lease{Duration: time.Second, Transitions: 2},
			wantAcquired: true, wantHolder: "me", wantTransitions: 3,
		},
		{
			name:         "renews its own lease, keeping its acquire time",
			existing:     &Lease{Holder: "me", Address: "me:8080", Duration: 15 * time.Second, AcquireTime: start.Add(-time.Hour), Transitions: 1},
			wait:         20 * time.Second,
			wantAcquired: true, wantHolder: "me", wantTransitions: 1, wantAcquireTime: start.Add(-time.Hour),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := &fakeClock{t: start}
			client := &fakeClient{}
			if tt.existing != nil {
				client.store(*tt.existing)
			}
			e := newElector(t, client, "me", Options{Now: clock.now})

			if tt.wait > 0 {
				// first observation starts the expiry clock
				if _, err := e.tryAcquireOrRenew(t.Context()); err != nil {
					t.Fatal(err)
				}
				clock.advance(tt.wait)
			}
			acquired, err := e.tryAcquireOrRenew(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			got := client.current()
			if acquired != tt.wantAcquired || got.Holder != tt.wantHolder || got.Transitions != tt.wantTransitions {
				t.Errorf("acquired %v, lease %+v; want acquired %v, holder %q, transitions %d",
					acquired, got, tt.wantAcquired, tt.wantHolder, tt.wantTransitions)
			}
			wantAcquire := tt.wantAcquireTime
			if wantAcquire.IsZero() {
				wantAcquire = clock.now()
			}
			if !got.AcquireTime.Equal(wantAcquire) {
				t.Errorf("acquire time %v, want %v", got.AcquireTime, wantAcquire)
			}
			if acquired && (got.Address != "me:8080" || !got.RenewTime.Equal(clock.now()) || got.Duration != DefaultLeaseDuration) {
				t.Errorf("acquired lease %+v, want address me:8080 renewed now for the default duration", got)
			}
		})
	}
}

func TestLeaderAddress(t *testing.T) {
	clock := &fakeClock{t: start}
	client := &fakeClient{}
	client.store(Lease{Holder: "other", Address: "10.0.0.2:8080", Duration: 15 * time.Second})
	e := newElector(t, client, "me", Options{Now: clock.now})

	if got := e.LeaderAddress(); got != "" {
		t.Errorf("before reading the lease, address = %q, want none", got)
	}
	if _, err := e.tryAcquireOrRenew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := e.LeaderAddress(); got != "10.0.0.2:8080" {
		t.Errorf("address = %q, want the holder's", got)
	}
	clock.advance(15 * time.Second)
	if got := e.LeaderAddress(); got != "" {
		t.Errorf("once expired, address = %q, want none", got)
	}
}

func TestHealthyTracksContactWithTheAPI(t *testing.T) {
	clock := &fakeClock{t: start}
	client := &fakeClient{}
	e := newElector(t, client, "me", Options{Now: clock.now})

	if e.Healthy() {
		t.Error("healthy before any request")
	}
	if _, err := e.tryAcquireOrRenew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !e.Healthy() {
		t.Error("not healthy after a successful request")
	}

	client.setErr(errors.New("connection refused"))
	clock.advance(DefaultLeaseDuration)
	if _, err := e.tryAcquireOrRenew(t.Context()); err == nil {
		t.Fatal("want an error")
	}
	if e.Healthy() {
		t.Error("healthy after failing for a lease duration")
	}
}

func TestNewRejectsRenewDeadlineNotShorterThanLease(t *testing.T) {
	_, err := New(&fakeClient{}, Options{Identity: "me", Address: "me:8080", LeaseDuration: time.Second, RenewDeadline: time.Second})
	if err == nil {
		t.Error("want an error")
	}
}

// fast timings for tests that run the election loop in real time
var fast = Options{LeaseDuration: 400 * time.Millisecond, RenewDeadline: 250 * time.Millisecond, RetryPeriod: 20 * time.Millisecond}

// recorder records leadership changes.
type recorder struct {
	mu      sync.Mutex
	changes []bool
}

func (r *recorder) record(leader bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, leader)
}

func (r *recorder) get() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.changes...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func run(e *Elector) (cancel func()) {
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.Run(ctx)
		close(done)
	}()
	return func() {
		stop()
		<-done
	}
}

func TestOneLeaderAndHandoverOnRelease(t *testing.T) {
	client := &fakeClient{}
	a := newElector(t, client, "a", fast)
	stopA := run(a)
	waitFor(t, "a leads", a.Leading)

	b := newElector(t, client, "b", fast)
	stopB := run(b)
	defer stopB()
	waitFor(t, "b sees a as leader", func() bool { return b.LeaderAddress() == "a:8080" })
	if b.Leading() {
		t.Fatal("b leads while a holds the lease")
	}

	released := time.Now()
	stopA()
	if a.Leading() {
		t.Error("a still leads after stopping")
	}
	waitFor(t, "b leads", b.Leading)
	// a released the lease, so b needn't wait for it to expire
	if took := time.Since(released); took >= fast.LeaseDuration {
		t.Errorf("b took %v to lead, want less than the lease duration", took)
	}
	if got := client.current(); got.Holder != "b" || got.Transitions != 1 {
		t.Errorf("lease = %+v, want held by b after one transition", got)
	}
}

func TestLosingTheLeaseCancelsHoldsAndCampaignsAgain(t *testing.T) {
	client := &fakeClient{}
	rec := &recorder{}
	opts := fast
	opts.OnChange = rec.record
	e := newElector(t, client, "me", opts)
	stop := run(e)
	defer stop()
	waitFor(t, "leading", e.Leading)

	held, done, ok := e.Hold(context.Background())
	if !ok {
		t.Fatal("hold refused while leading")
	}

	client.setErr(errors.New("API server unavailable"))
	select {
	case <-held.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("hold not cancelled after renewal failed")
	}
	if _, _, ok := e.Hold(context.Background()); ok {
		t.Error("new hold allowed after leadership was lost")
	}
	// stepping down waits for the hold to finish
	time.Sleep(50 * time.Millisecond)
	if got := rec.get(); len(got) != 1 {
		t.Errorf("changes = %v before the hold finished, want only becoming leader", got)
	}
	done()

	client.setErr(nil)
	waitFor(t, "leading again", e.Leading)
	if got := rec.get(); len(got) != 3 || !got[0] || got[1] || !got[2] {
		t.Errorf("changes = %v, want leader, standby, leader", got)
	}
}

func TestHoldIsRefusedWhenNotLeading(t *testing.T) {
	e := newElector(t, &fakeClient{}, "me", Options{})
	if _, _, ok := e.Hold(context.Background()); ok {
		t.Error("hold allowed before leading")
	}
}
