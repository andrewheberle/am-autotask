package selector

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

// Source loads every company that has company selectors set.
type Source func(ctx context.Context) ([]Company, error)

// CacheOptions configures a [Cache]. Zero values are replaced by defaults.
type CacheOptions struct {
	// MissRefreshInterval is the minimum time between refreshes triggered
	// by an alert group matching no company. Default one minute.
	MissRefreshInterval time.Duration

	// Now returns the current time. Default [time.Now].
	Now func() time.Time

	// Logger receives warnings about invalid selectors and failed
	// refreshes. Default [slog.Default].
	Logger *slog.Logger
}

// Cache holds the most recent successfully loaded [Set] in memory so alert
// groups can be matched without calling Autotask.
type Cache struct {
	source Source
	opts   CacheOptions

	mu              sync.RWMutex
	set             *Set
	invalid         int
	loadedAt        time.Time
	lastMissRefresh time.Time

	// serialises refreshes so concurrent misses do not stampede Autotask
	refreshMu sync.Mutex
}

// NewCache returns an empty cache; call [Cache.Load] before use.
func NewCache(source Source, opts CacheOptions) *Cache {
	if opts.MissRefreshInterval == 0 {
		opts.MissRefreshInterval = time.Minute
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Cache{source: source, opts: opts, set: &Set{}}
}

// Load performs the initial load. Unlike [Cache.Refresh], its error is
// intended to stop startup.
func (c *Cache) Load(ctx context.Context) error {
	if err := c.Refresh(ctx); err != nil {
		return fmt.Errorf("initial company selector load: %w", err)
	}
	return nil
}

// Refresh reloads the company selectors. On failure the previous set is
// kept.
func (c *Cache) Refresh(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	return c.refresh(ctx)
}

func (c *Cache) refresh(ctx context.Context) error {
	companies, err := c.source(ctx)
	if err != nil {
		return err
	}

	set, errs := Build(companies)
	for _, err := range errs {
		c.opts.Logger.WarnContext(ctx, "skipping company with invalid company selectors", "error", err)
	}

	c.mu.Lock()
	c.set = set
	c.invalid = len(errs)
	c.loadedAt = c.opts.Now()
	c.mu.Unlock()

	return nil
}

// Match returns the IDs of the companies whose selectors match the group
// labels. When nothing matches it refreshes first, at most once per
// MissRefreshInterval, so newly tagged companies are found promptly.
func (c *Cache) Match(ctx context.Context, groupLabels map[string]string) []int64 {
	if ids := c.current().Match(groupLabels); ids != nil {
		return ids
	}

	if !c.missRefreshDue() {
		return nil
	}
	if err := c.Refresh(ctx); err != nil {
		c.opts.Logger.WarnContext(ctx, "company selector refresh after no match failed", "error", err)
	}

	return c.current().Match(groupLabels)
}

func (c *Cache) missRefreshDue() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.opts.Now()
	if !c.lastMissRefresh.IsZero() && now.Sub(c.lastMissRefresh) < c.opts.MissRefreshInterval {
		return false
	}
	c.lastMissRefresh = now
	return true
}

func (c *Cache) current() *Set {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.set
}

// Counts returns the number of companies whose selectors were valid, and
// invalid and so skipped, in the last successful load.
func (c *Cache) Counts() (valid, invalid int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.set.Len(), c.invalid
}

// Age returns the time since the last successful load.
func (c *Cache) Age() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.opts.Now().Sub(c.loadedAt)
}

// Run refreshes the cache about every interval until ctx is cancelled.
// Failures are logged and the previous set kept. The first refresh comes
// after a random part of the interval and each later one varies by up to
// 10%, so replicas started together don't keep refreshing together.
func (c *Cache) Run(ctx context.Context, interval time.Duration) {
	delay := firstDelay(interval, rand.Float64())
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			if err := c.Refresh(ctx); err != nil {
				c.opts.Logger.WarnContext(ctx, "company selector refresh failed", "error", err)
			}
		}
		delay = nextDelay(interval, rand.Float64())
	}
}

// firstDelay is a random part of interval, given r in [0, 1).
func firstDelay(interval time.Duration, r float64) time.Duration {
	return time.Duration(float64(interval) * r)
}

// nextDelay is interval varied by up to 10% either way, given r in [0, 1).
func nextDelay(interval time.Duration, r float64) time.Duration {
	return time.Duration(float64(interval) * (0.9 + 0.2*r))
}
