package tracing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/andrewheberle/am-autotask/internal/tracing"
)

func TestLogHandlerAddsTraceAndSpanIDsInsideSpans(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(tracing.LogHandler(slog.NewJSONHandler(&out, nil))).With("component", "test")
	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("test").Start(context.Background(), "test span")

	logger.InfoContext(ctx, "inside")
	span.End()
	logger.InfoContext(context.Background(), "outside")

	dec := json.NewDecoder(&out)
	var inside, outside map[string]any
	if err := dec.Decode(&inside); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&outside); err != nil {
		t.Fatal(err)
	}

	sc := span.SpanContext()
	if inside["trace_id"] != sc.TraceID().String() || inside["span_id"] != sc.SpanID().String() {
		t.Errorf("log inside span = %v, want trace_id %s and span_id %s", inside, sc.TraceID(), sc.SpanID())
	}
	if inside["component"] != "test" {
		t.Errorf("attributes added with With should be kept: %v", inside)
	}
	if _, ok := outside["trace_id"]; ok {
		t.Errorf("log outside a span should have no trace_id: %v", outside)
	}
}
