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

// Handler applies a notification. It is satisfied by [receiver.Receiver].
type Handler interface {
	Handle(ctx context.Context, n receiver.Notification) error
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

	err = s.h.Handle(r.Context(), n)
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
