package tracing_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/tracing"
)

func TestErrorHandlerLogsFirstThenAtMostOncePerInterval(t *testing.T) {
	var logs bytes.Buffer
	now := time.Unix(0, 0)
	h := tracing.NewErrorHandler(slog.New(slog.NewTextHandler(&logs, nil)), time.Minute, func() time.Time { return now })

	h.Handle(errors.New("export failed: connection refused"))
	for range 5 {
		now = now.Add(10 * time.Second)
		h.Handle(errors.New("export failed: connection refused"))
	}
	now = now.Add(20 * time.Second)
	h.Handle(errors.New("export failed: timeout"))

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two log lines, got %d:\n%s", len(lines), logs.String())
	}
	if !strings.Contains(lines[0], "level=WARN") || !strings.Contains(lines[0], "connection refused") {
		t.Errorf("first error should be logged at WARN straight away: %s", lines[0])
	}
	if !strings.Contains(lines[1], "timeout") || !strings.Contains(lines[1], "suppressed=5") {
		t.Errorf("next log should carry the latest error and the count suppressed: %s", lines[1])
	}
}
