package tracing_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/andrewheberle/am-autotask/internal/tracing"
)

// fakeCollector is an OTLP/HTTP trace receiver.
type fakeCollector struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []*coltracepb.ExportTraceServiceRequest
}

func newFakeCollector(t *testing.T) *fakeCollector {
	t.Helper()
	c := &fakeCollector{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.requests = append(c.requests, &req)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(nil)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// received returns the names of the spans received and the resource
// attributes they were exported with.
func (c *fakeCollector) received() (names []string, resource map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	resource = map[string]string{}
	for _, req := range c.requests {
		for _, rs := range req.ResourceSpans {
			for _, kv := range rs.Resource.Attributes {
				resource[kv.Key] = kv.Value.GetStringValue()
			}
			for _, ss := range rs.ScopeSpans {
				for _, s := range ss.Spans {
					names = append(names, s.Name)
				}
			}
		}
	}
	return names, resource
}

func emitSpan(t *testing.T, p *tracing.Provider, name string) {
	t.Helper()
	_, span := p.TracerProvider.Tracer("test").Start(context.Background(), name)
	span.End()
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestOTLPExportsSpansWithServiceResource(t *testing.T) {
	c := newFakeCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", c.srv.URL)

	p, err := tracing.Setup(context.Background(), tracing.Options{Exporter: "otlp", ServiceVersion: "v1.2.3"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	emitSpan(t, p, "test span")

	names, resource := c.received()
	if len(names) != 1 || names[0] != "test span" {
		t.Errorf("spans received = %v, want [test span]", names)
	}
	if resource["service.name"] != "am-autotask" || resource["service.version"] != "v1.2.3" {
		t.Errorf("resource = %v, want service.name am-autotask and service.version v1.2.3", resource)
	}
}

func TestOTELServiceNameOverridesDefault(t *testing.T) {
	c := newFakeCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", c.srv.URL)
	t.Setenv("OTEL_SERVICE_NAME", "am-autotask-prod")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=prod")

	p, err := tracing.Setup(context.Background(), tracing.Options{Exporter: "otlp", ServiceVersion: "v1.2.3"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	emitSpan(t, p, "test span")

	_, resource := c.received()
	if resource["service.name"] != "am-autotask-prod" || resource["deployment.environment.name"] != "prod" {
		t.Errorf("resource = %v, want OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES applied", resource)
	}
}

func TestNoneExportsNothing(t *testing.T) {
	c := newFakeCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", c.srv.URL)

	p, err := tracing.Setup(context.Background(), tracing.Options{Exporter: "none"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	_, span := p.TracerProvider.Tracer("test").Start(context.Background(), "test span")
	recording := span.IsRecording()
	span.End()
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	if recording {
		t.Error("spans should not record when tracing is off")
	}
	if names, _ := c.received(); len(names) != 0 {
		t.Errorf("expected nothing exported, got %v", names)
	}
}

func TestConsoleWritesSpans(t *testing.T) {
	var out bytes.Buffer
	p, err := tracing.Setup(context.Background(), tracing.Options{Exporter: "console", ConsoleWriter: &out})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	emitSpan(t, p, "test span")

	if !strings.Contains(out.String(), `"Name":"test span"`) {
		t.Errorf("console output should contain the span:\n%s", out.String())
	}
}

func TestOnlyHTTPProtobufIsSupported(t *testing.T) {
	for _, env := range []string{"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "grpc")

			_, err := tracing.Setup(context.Background(), tracing.Options{Exporter: "otlp"})

			if err == nil || !strings.Contains(err.Error(), "http/protobuf") || !strings.Contains(err.Error(), env) {
				t.Errorf("err = %v, want one naming %s and http/protobuf", err, env)
			}
		})
	}

	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	if p, err := tracing.Setup(context.Background(), tracing.Options{Exporter: "otlp"}); err != nil {
		t.Errorf("http/protobuf should be accepted: %v", err)
	} else {
		_ = p.Shutdown(context.Background())
	}
}

func TestUnknownExporterIsRejected(t *testing.T) {
	if _, err := tracing.Setup(context.Background(), tracing.Options{Exporter: "jaeger"}); err == nil {
		t.Error("expected an error for an unknown exporter")
	}
}

func TestPropagatorsReadW3CTraceContext(t *testing.T) {
	p, err := tracing.Setup(context.Background(), tracing.Options{Exporter: "none"})
	if err != nil {
		t.Fatal(err)
	}

	fields := p.Propagators.Fields()
	if !contains(fields, "traceparent") || !contains(fields, "baggage") {
		t.Errorf("propagator fields = %v, want traceparent and baggage", fields)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
