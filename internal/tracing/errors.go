package tracing

import (
	"log/slog"
	"sync"
	"time"
)

// ErrorHandler logs OpenTelemetry's internal errors, such as failed span
// exports, at WARN: the first straight away, then at most once per interval
// with the number suppressed since. Install it with otel.SetErrorHandler.
type ErrorHandler struct {
	logger   *slog.Logger
	interval time.Duration
	now      func() time.Time

	mu         sync.Mutex
	last       time.Time
	suppressed int
}

// NewErrorHandler returns an [ErrorHandler]. now defaults to [time.Now].
func NewErrorHandler(logger *slog.Logger, interval time.Duration, now func() time.Time) *ErrorHandler {
	if now == nil {
		now = time.Now
	}
	return &ErrorHandler{logger: logger, interval: interval, now: now}
}

// Handle implements otel.ErrorHandler.
func (h *ErrorHandler) Handle(err error) {
	h.mu.Lock()
	t := h.now()
	if !h.last.IsZero() && t.Sub(h.last) < h.interval {
		h.suppressed++
		h.mu.Unlock()
		return
	}
	suppressed := h.suppressed
	h.last, h.suppressed = t, 0
	h.mu.Unlock()

	h.logger.Warn("tracing error; spans may be lost", "error", err, "suppressed", suppressed)
}
