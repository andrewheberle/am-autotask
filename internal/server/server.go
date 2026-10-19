// Package server exposes the Alertmanager webhook endpoint and health checks.
package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/andrewheberle/am-autotask/internal/receiver"
)

// maxBody bounds the size of a notification.
const maxBody = 10 << 20

// supportedVersion is the Alertmanager webhook payload version understood.
const supportedVersion = "4"

// Notification results reported to Options.Observe.
const (
	ResultHandled      = "handled"
	ResultUnauthorized = "unauthorized"
	ResultInvalid      = "invalid"
	ResultTemporary    = "temporary_failure"
	ResultPermanent    = "permanent_failure"
)

// Results of forwarding a notification to the leader, reported to
// Options.ObserveForward.
const (
	// ForwardDelivered means the leader answered with success.
	ForwardDelivered = "delivered"
	// ForwardLeaderError means the leader answered with an error, which is
	// passed back so Alertmanager retries.
	ForwardLeaderError = "leader_error"
	// ForwardNoLeader means no replica holds the lease.
	ForwardNoLeader = "no_leader"
	// ForwardUnreachable means the leader could not be reached.
	ForwardUnreachable = "unreachable"
)

// ForwardResults lists every forwarding result.
var ForwardResults = []string{ForwardDelivered, ForwardLeaderError, ForwardNoLeader, ForwardUnreachable}

// ForwardedHeader marks a notification forwarded by a standby, so a replica
// that is no longer the leader rejects it rather than forwarding it again.
const ForwardedHeader = "X-Am-Autotask-Forwarded"

// Handler applies a notification. It is satisfied by [receiver.Receiver].
type Handler interface {
	Handle(ctx context.Context, n receiver.Notification) error
}

// Leadership decides which replica handles notifications. It is satisfied
// by [leader.Elector].
type Leadership interface {
	// Hold returns a context for handling a notification as leader,
	// cancelled if leadership is lost, and a function to call when done. ok
	// is false if this replica is not the leader.
	Hold(ctx context.Context) (held context.Context, done func(), ok bool)
	// LeaderAddress returns the leader's host:port, or "" if there is none.
	LeaderAddress() string
}

// Options configures the server.
type Options struct {
	// Token is the bearer token Alertmanager must present. Required.
	Token string
	// Ready reports whether startup has completed, for /readyz.
	Ready func() bool
	// Metrics, when set, is served at /metrics.
	Metrics http.Handler
	// Observe, when set, is called with the result of every webhook request.
	Observe func(result string)
	// Logger defaults to [slog.Default].
	Logger *slog.Logger
	// TracerProvider traces webhook requests. Default the global provider.
	TracerProvider trace.TracerProvider
	// Propagators read the trace context Alertmanager sends. Default the
	// global propagators.
	Propagators propagation.TextMapPropagator

	// Leadership, when set, limits handling to the leader: other replicas
	// forward notifications to it.
	Leadership Leadership
	// ForwardClient sends forwarded notifications. Default
	// [http.DefaultClient].
	ForwardClient *http.Client
	// ObserveForward, when set, is called with the result of every
	// notification forwarded to the leader.
	ObserveForward func(result string)
}

type server struct {
	h           Handler
	opts        Options
	versionOnce sync.Once
}

// New returns the HTTP handler serving /webhook, /healthz, /readyz and,
// when configured, /metrics.
func New(h Handler, opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Ready == nil {
		opts.Ready = func() bool { return true }
	}
	if opts.Observe == nil {
		opts.Observe = func(string) {}
	}
	if opts.TracerProvider == nil {
		opts.TracerProvider = otel.GetTracerProvider()
	}
	if opts.Propagators == nil {
		opts.Propagators = otel.GetTextMapPropagator()
	}
	if opts.ForwardClient == nil {
		opts.ForwardClient = http.DefaultClient
	}
	if opts.ObserveForward == nil {
		opts.ObserveForward = func(string) {}
	}
	s := &server{h: h, opts: opts}

	mux := http.NewServeMux()
	// only notifications are traced, continuing Alertmanager's trace
	mux.Handle("POST /webhook", otelhttp.NewHandler(http.HandlerFunc(s.webhook), "",
		otelhttp.WithTracerProvider(opts.TracerProvider),
		otelhttp.WithPropagators(opts.Propagators),
		otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return "POST /webhook" }),
	))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !opts.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if opts.Metrics != nil {
		mux.Handle("GET /metrics", opts.Metrics)
	}

	return mux
}

func (s *server) authorised(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.opts.Token)) == 1
}

func (s *server) webhook(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		s.opts.Observe(ResultUnauthorized)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		s.opts.Observe(ResultInvalid)
		http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var n receiver.Notification
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&n); err != nil {
		s.opts.Observe(ResultInvalid)
		http.Error(w, "invalid notification: "+err.Error(), http.StatusBadRequest)
		return
	}
	if n.Version != supportedVersion {
		s.versionOnce.Do(func() {
			s.opts.Logger.ErrorContext(r.Context(), "rejecting notifications with unsupported webhook version",
				"version", n.Version, "supported", supportedVersion)
		})
		s.opts.Observe(ResultInvalid)
		http.Error(w, "unsupported webhook version "+n.Version, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if s.opts.Leadership != nil {
		held, done, ok := s.opts.Leadership.Hold(ctx)
		if !ok {
			s.forward(w, r, body)
			return
		}
		defer done()
		ctx = held
	}

	err = s.h.Handle(ctx, n)
	switch {
	case err == nil:
		s.opts.Observe(ResultHandled)
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, receiver.ErrPermanent):
		// retrying cannot help, so acknowledge it and leave a record
		s.opts.Logger.ErrorContext(r.Context(), "notification failed permanently; not retrying",
			"error", err, "groupKey", n.GroupKey, "payload", string(body))
		s.opts.Observe(ResultPermanent)
		w.WriteHeader(http.StatusOK)
	default:
		s.opts.Logger.WarnContext(r.Context(), "notification failed; Alertmanager will retry", "error", err, "groupKey", n.GroupKey)
		s.opts.Observe(ResultTemporary)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// forward passes a notification to the leader and its answer back to
// Alertmanager. Any failure is a 5xx, so Alertmanager retries.
func (s *server) forward(w http.ResponseWriter, r *http.Request, body []byte) {
	ctx := r.Context()
	if r.Header.Get(ForwardedHeader) != "" {
		// the sender thought this replica led, but leadership has moved
		s.opts.Observe(ResultTemporary)
		http.Error(w, "not the leader", http.StatusServiceUnavailable)
		return
	}

	addr := s.opts.Leadership.LeaderAddress()
	if addr == "" {
		s.opts.ObserveForward(ForwardNoLeader)
		s.opts.Logger.WarnContext(ctx, "no leader to forward the notification to; Alertmanager will retry")
		http.Error(w, "no leader", http.StatusServiceUnavailable)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/webhook", bytes.NewReader(body))
	if err != nil {
		s.opts.ObserveForward(ForwardUnreachable)
		http.Error(w, "forwarding to leader: "+err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ForwardedHeader, "1")

	resp, err := s.opts.ForwardClient.Do(req)
	if err != nil {
		s.opts.ObserveForward(ForwardUnreachable)
		s.opts.Logger.WarnContext(ctx, "forwarding the notification to the leader failed; Alertmanager will retry", "leader", addr, "error", err)
		http.Error(w, "forwarding to leader: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 300 {
		s.opts.ObserveForward(ForwardDelivered)
	} else {
		s.opts.ObserveForward(ForwardLeaderError)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxBody))
}
