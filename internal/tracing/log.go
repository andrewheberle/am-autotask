package tracing

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// LogHandler wraps h so records logged with a context holding a span carry
// its trace_id and span_id.
func LogHandler(h slog.Handler) slog.Handler {
	return logHandler{h}
}

type logHandler struct {
	slog.Handler
}

func (l logHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return l.Handler.Handle(ctx, r)
}

func (l logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{l.Handler.WithAttrs(attrs)}
}

func (l logHandler) WithGroup(name string) slog.Handler {
	return logHandler{l.Handler.WithGroup(name)}
}
