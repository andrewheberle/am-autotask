// Package tracing sets up OpenTelemetry tracing for the receiver.
//
// Tracing is switched on by the exporter setting; everything else (the OTLP
// endpoint, headers and TLS, the sampler and resource attributes) comes from
// the standard OTEL_* environment variables. See ADR-0003.
package tracing

import (
	"context"
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ServiceName is the default service.name, overridden by OTEL_SERVICE_NAME.
const ServiceName = "am-autotask"

// Exporter values.
const (
	ExporterNone    = "none"
	ExporterOTLP    = "otlp"
	ExporterConsole = "console"
)

// Options configures [Setup].
type Options struct {
	// Exporter is ExporterNone, ExporterOTLP or ExporterConsole.
	Exporter string
	// ServiceVersion is recorded as service.version.
	ServiceVersion string
	// ConsoleWriter receives spans for ExporterConsole. Default os.Stdout.
	ConsoleWriter io.Writer
}

// Provider is the tracing set up by [Setup].
type Provider struct {
	TracerProvider trace.TracerProvider
	Propagators    propagation.TextMapPropagator
	// Shutdown flushes buffered spans and stops exporting.
	Shutdown func(context.Context) error
}

// supportedProtocol is the only OTLP protocol supported.
const supportedProtocol = "http/protobuf"

// Setup builds a tracer provider for the exporter. It does not change
// OpenTelemetry's global state.
func Setup(ctx context.Context, opts Options) (*Provider, error) {
	p := &Provider{
		Propagators: propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}),
		Shutdown:    func(context.Context) error { return nil },
	}

	var exporter sdktrace.SpanExporter
	switch opts.Exporter {
	case ExporterNone, "":
		p.TracerProvider = noop.NewTracerProvider()
		return p, nil
	case ExporterOTLP:
		if err := checkProtocol(); err != nil {
			return nil, err
		}
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("creating OTLP trace exporter: %w", err)
		}
		exporter = exp
	case ExporterConsole:
		w := opts.ConsoleWriter
		if w == nil {
			w = os.Stdout
		}
		exp, err := stdouttrace.New(stdouttrace.WithWriter(w))
		if err != nil {
			return nil, fmt.Errorf("creating console trace exporter: %w", err)
		}
		exporter = exp
	default:
		return nil, fmt.Errorf("unknown tracing exporter %q: use %s, %s or %s", opts.Exporter, ExporterNone, ExporterOTLP, ExporterConsole)
	}

	res, err := newResource(ctx, opts.ServiceVersion)
	if err != nil {
		return nil, err
	}

	// the sampler comes from OTEL_TRACES_SAMPLER, defaulting to
	// parentbased_always_on
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	p.TracerProvider = tp
	p.Shutdown = tp.Shutdown
	return p, nil
}

// checkProtocol rejects any OTLP protocol other than http/protobuf rather
// than send HTTP to, for example, a gRPC endpoint.
func checkProtocol() error {
	for _, env := range []string{"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL"} {
		v, ok := os.LookupEnv(env)
		if !ok {
			continue
		}
		if v != supportedProtocol {
			return fmt.Errorf("%s=%q is not supported: only %s is", env, v, supportedProtocol)
		}
		// the traces-specific variable takes precedence
		return nil
	}
	return nil
}

// newResource describes the service, with OTEL_SERVICE_NAME and
// OTEL_RESOURCE_ATTRIBUTES taking precedence over the defaults.
func newResource(ctx context.Context, version string) (*resource.Resource, error) {
	defaults := []attribute.KeyValue{attribute.String("service.name", ServiceName)}
	if version != "" {
		defaults = append(defaults, attribute.String("service.version", version))
	}

	fromEnv, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return nil, fmt.Errorf("reading resource attributes: %w", err)
	}

	res, err := resource.Merge(resource.NewSchemaless(defaults...), fromEnv)
	if err != nil {
		return nil, fmt.Errorf("building resource: %w", err)
	}
	return res, nil
}
