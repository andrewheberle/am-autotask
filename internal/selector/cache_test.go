package selector_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/selector"
)

// fakeSource stands in for Autotask's company query.
type fakeSource struct {
	mu        sync.Mutex
	companies []selector.Company
	err       error
}

func (f *fakeSource) set(companies []selector.Company, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.companies, f.err = companies, err
}

func (f *fakeSource) load(context.Context) ([]selector.Company, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.companies), f.err
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newCache(t *testing.T, src *fakeSource, clock *fakeClock) *selector.Cache {
	t.Helper()
	return selector.NewCache(src.load, selector.CacheOptions{
		MissRefreshInterval: time.Minute,
		Now:                 clock.now,
	})
}

var (
	fooLabels = map[string]string{"tenant_id": "foo"}
	fooCo     = selector.Company{ID: 1, Selectors: `{tenant_id="foo"}`}
	barLabels = map[string]string{"tenant_id": "bar"}
	barCo     = selector.Company{ID: 2, Selectors: `{tenant_id="bar"}`}
)

func TestCacheInitialLoadFailureIsReported(t *testing.T) {
	src := &fakeSource{err: errors.New("autotask down")}
	cache := newCache(t, src, &fakeClock{t: time.Unix(0, 0)})

	if err := cache.Load(context.Background()); err == nil {
		t.Fatal("expected initial load to fail")
	}
}

func TestCacheMatchesLoadedSelectors(t *testing.T) {
	src := &fakeSource{companies: []selector.Company{fooCo}}
	cache := newCache(t, src, &fakeClock{t: time.Unix(0, 0)})
	if err := cache.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := cache.Match(context.Background(), fooLabels); !slices.Equal(got, []int64{1}) {
		t.Errorf("Match = %v, want [1]", got)
	}
}

func TestCacheKeepsLastGoodSetWhenRefreshFails(t *testing.T) {
	src := &fakeSource{companies: []selector.Company{fooCo}}
	cache := newCache(t, src, &fakeClock{t: time.Unix(0, 0)})
	if err := cache.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	src.set(nil, errors.New("autotask down"))
	if err := cache.Refresh(context.Background()); err == nil {
		t.Fatal("expected refresh to fail")
	}

	if got := cache.Match(context.Background(), fooLabels); !slices.Equal(got, []int64{1}) {
		t.Errorf("Match after failed refresh = %v, want [1]", got)
	}
}

func TestCacheRefreshPicksUpChanges(t *testing.T) {
	src := &fakeSource{companies: []selector.Company{fooCo}}
	cache := newCache(t, src, &fakeClock{t: time.Unix(0, 0)})
	if err := cache.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	src.set([]selector.Company{barCo}, nil)
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := cache.Match(context.Background(), barLabels); !slices.Equal(got, []int64{2}) {
		t.Errorf("Match = %v, want [2]", got)
	}
}

func TestCacheMissRefreshesAtMostOncePerInterval(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	src := &fakeSource{companies: []selector.Company{fooCo}}
	cache := newCache(t, src, clock)
	if err := cache.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	// a company tagged after startup is found by the miss-triggered refresh
	clock.advance(time.Minute)
	src.set([]selector.Company{fooCo, barCo}, nil)
	if got := cache.Match(context.Background(), barLabels); !slices.Equal(got, []int64{2}) {
		t.Fatalf("Match after miss = %v, want [2]", got)
	}

	// a second miss inside the interval does not refresh again
	src.set([]selector.Company{fooCo, barCo, {ID: 3, Selectors: `{tenant_id="baz"}`}}, nil)
	clock.advance(30 * time.Second)
	if got := cache.Match(context.Background(), map[string]string{"tenant_id": "baz"}); got != nil {
		t.Fatalf("Match within miss interval = %v, want no match", got)
	}

	// once the interval has passed a miss refreshes again
	clock.advance(30 * time.Second)
	if got := cache.Match(context.Background(), map[string]string{"tenant_id": "baz"}); !slices.Equal(got, []int64{3}) {
		t.Errorf("Match after interval = %v, want [3]", got)
	}
}

func TestCacheAgeTracksLastSuccessfulLoad(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	src := &fakeSource{companies: []selector.Company{fooCo}}
	cache := newCache(t, src, clock)
	if err := cache.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	clock.advance(10 * time.Minute)
	src.set(nil, errors.New("autotask down"))
	_ = cache.Refresh(context.Background())

	if got := cache.Age(); got != 10*time.Minute {
		t.Errorf("Age = %v, want 10m", got)
	}
}

func TestCacheCountsValidAndInvalidCompaniesOfLastLoad(t *testing.T) {
	src := &fakeSource{companies: []selector.Company{fooCo, barCo, {ID: 3, Selectors: `{tenant_id="baz"`}}}
	cache := newCache(t, src, &fakeClock{t: time.Unix(0, 0)})
	if err := cache.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	if valid, invalid := cache.Counts(); valid != 2 || invalid != 1 {
		t.Errorf("Counts = %d valid, %d invalid; want 2, 1", valid, invalid)
	}

	// a failed refresh keeps the counts of the set still in use
	src.set(nil, errors.New("autotask down"))
	_ = cache.Refresh(context.Background())
	if valid, invalid := cache.Counts(); valid != 2 || invalid != 1 {
		t.Errorf("Counts after failed refresh = %d valid, %d invalid; want 2, 1", valid, invalid)
	}

	src.set([]selector.Company{fooCo}, nil)
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if valid, invalid := cache.Counts(); valid != 1 || invalid != 0 {
		t.Errorf("Counts after refresh = %d valid, %d invalid; want 1, 0", valid, invalid)
	}
}
