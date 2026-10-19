package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/server"
)

// fakeLeadership is a fixed leadership state.
type fakeLeadership struct {
	leading bool
	address string
	lost    bool
}

type heldKey struct{}

func (f *fakeLeadership) Hold(ctx context.Context) (context.Context, func(), bool) {
	if !f.leading {
		return nil, nil, false
	}
	held, cancel := context.WithCancel(context.WithValue(ctx, heldKey{}, true))
	if f.lost {
		cancel()
	}
	return held, cancel, true
}

func (f *fakeLeadership) LeaderAddress() string { return f.address }

// ctxHandler records the context each notification is handled with.
type ctxHandler struct {
	mu   sync.Mutex
	ctxs []context.Context
	err  error
}

func (h *ctxHandler) Handle(ctx context.Context, _ receiver.Notification) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctxs = append(h.ctxs, ctx)
	if h.err != nil {
		return h.err
	}
	return ctx.Err()
}

func (h *ctxHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.ctxs)
}

// replica is one am-autotask server in a forwarding test.
type replica struct {
	*httptest.Server
	handler    *ctxHandler
	leadership *fakeLeadership
	results    []string
	forwarded  []string
	headers    []http.Header
}

func newReplica(t *testing.T, leadership *fakeLeadership) *replica {
	t.Helper()
	r := &replica{handler: &ctxHandler{}, leadership: leadership}
	h := server.New(r.handler, server.Options{
		Token:          token,
		Logger:         slog.New(slog.DiscardHandler),
		Leadership:     leadership,
		Observe:        func(result string) { r.results = append(r.results, result) },
		ObserveForward: func(result string) { r.forwarded = append(r.forwarded, result) },
	})
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.headers = append(r.headers, req.Header.Clone())
		h.ServeHTTP(w, req)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *replica) address() string {
	return strings.TrimPrefix(r.URL, "http://")
}

func post(t *testing.T, url string, header http.Header) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/webhook", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func bearer() http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func TestLeaderHandlesWithItsLeadershipContext(t *testing.T) {
	tests := []struct {
		name       string
		lost       bool
		wantStatus int
	}{
		{name: "holding", wantStatus: http.StatusOK},
		{name: "leadership lost while handling", lost: true, wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leader := newReplica(t, &fakeLeadership{leading: true, lost: tt.lost})

			status, _ := post(t, leader.URL, bearer())

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if leader.handler.count() != 1 || leader.handler.ctxs[0].Value(heldKey{}) == nil {
				t.Error("notification not handled with the held context")
			}
			if len(leader.forwarded) != 0 {
				t.Errorf("leader forwarded %v", leader.forwarded)
			}
		})
	}
}

func TestStandbyForwardsToLeader(t *testing.T) {
	tests := []struct {
		name          string
		leaderErr     error
		wantStatus    int
		wantForwarded string
	}{
		{name: "leader handles it", wantStatus: http.StatusOK, wantForwarded: server.ForwardDelivered},
		{name: "leader fails", leaderErr: errors.New("autotask timed out"), wantStatus: http.StatusInternalServerError, wantForwarded: server.ForwardLeaderError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leader := newReplica(t, &fakeLeadership{leading: true})
			leader.handler.err = tt.leaderErr
			standby := newReplica(t, &fakeLeadership{address: leader.address()})

			status, body := post(t, standby.URL, bearer())

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if tt.leaderErr != nil && !strings.Contains(body, tt.leaderErr.Error()) {
				t.Errorf("body = %q, want the leader's error", body)
			}
			if standby.handler.count() != 0 || leader.handler.count() != 1 {
				t.Errorf("handled %d times on the standby and %d on the leader, want only once on the leader", standby.handler.count(), leader.handler.count())
			}
			if len(standby.forwarded) != 1 || standby.forwarded[0] != tt.wantForwarded {
				t.Errorf("standby forwarding results = %v, want %s", standby.forwarded, tt.wantForwarded)
			}
			if len(standby.results) != 0 {
				t.Errorf("standby counted notification results %v; the leader counts them", standby.results)
			}
			h := leader.headers[0]
			if h.Get("Authorization") != "Bearer "+token || h.Get(server.ForwardedHeader) == "" {
				t.Errorf("leader received headers %v, want the token and the forwarded marker", h)
			}
		})
	}
}

func TestStandbyAsksForRetryWithoutAReachableLeader(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	goneAddr := strings.TrimPrefix(gone.URL, "http://")
	gone.Close()

	tests := []struct {
		name          string
		address       string
		wantStatus    int
		wantForwarded string
	}{
		{name: "no leader", wantStatus: http.StatusServiceUnavailable, wantForwarded: server.ForwardNoLeader},
		{name: "leader unreachable", address: goneAddr, wantStatus: http.StatusBadGateway, wantForwarded: server.ForwardUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			standby := newReplica(t, &fakeLeadership{address: tt.address})

			status, _ := post(t, standby.URL, bearer())

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if len(standby.forwarded) != 1 || standby.forwarded[0] != tt.wantForwarded {
				t.Errorf("forwarding results = %v, want %s", standby.forwarded, tt.wantForwarded)
			}
		})
	}
}

func TestForwardedNotificationIsNotForwardedAgain(t *testing.T) {
	other := newReplica(t, &fakeLeadership{leading: true})
	// this replica lost the lease after the sender read it
	former := newReplica(t, &fakeLeadership{address: other.address()})

	h := bearer()
	h.Set(server.ForwardedHeader, "1")
	status, _ := post(t, former.URL, h)

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if other.handler.count() != 0 || len(former.forwarded) != 0 {
		t.Error("forwarded notification was forwarded again")
	}
	if len(former.results) != 1 || former.results[0] != server.ResultTemporary {
		t.Errorf("results = %v, want a temporary failure", former.results)
	}
}

func TestStandbyChecksTheTokenBeforeForwarding(t *testing.T) {
	leader := newReplica(t, &fakeLeadership{leading: true})
	standby := newReplica(t, &fakeLeadership{address: leader.address()})

	status, _ := post(t, standby.URL, http.Header{"Authorization": {"Bearer wrong"}})

	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	if len(leader.headers) != 0 {
		t.Error("unauthorised notification was forwarded")
	}
}
