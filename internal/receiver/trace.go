package receiver

import (
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracerScope is the instrumentation scope of the receiver's spans.
const TracerScope = "github.com/andrewheberle/am-autotask"

// Outcomes of handling a notification, recorded as am_autotask.outcome.
const (
	OutcomeCreated   = "created"
	OutcomeUpdated   = "updated"
	OutcomeUnchanged = "unchanged"
	OutcomeIgnored   = "ignored"
)

// Failure kinds recorded as am_autotask.failure.
const (
	FailureTemporary = "temporary"
	FailurePermanent = "permanent"
)

// EndSpan records err, if any, on span as an error of its failure kind
// (permanent if it wraps [ErrPermanent], otherwise temporary), then ends
// the span.
func EndSpan(span trace.Span, err error) {
	if err != nil {
		kind := FailureTemporary
		if errors.Is(err, ErrPermanent) {
			kind = FailurePermanent
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String("am_autotask.failure", kind))
	}
	span.End()
}
