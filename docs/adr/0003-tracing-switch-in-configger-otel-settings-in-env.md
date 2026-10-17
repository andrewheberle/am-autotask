# Tracing is switched on through configger and configured through OTEL_* variables

Tracing is off unless the configger setting `tracing.exporter` is `otlp` or `console`. Everything else about it (the OTLP protocol, endpoint, headers and TLS, the sampler, and resource attributes) comes from the standard `OTEL_*` environment variables, which the OpenTelemetry Go SDK and exporters already read. We split it this way so the one decision an operator must make sits with the receiver's other settings, while the rest follows the OpenTelemetry specification that collectors, operators and documentation already assume.

## Considered Options

- **Only `OTEL_*` variables, through `contrib/exporters/autoexport`**: fully standard, but autoexport exports to `localhost:4318` when `OTEL_TRACES_EXPORTER` is unset, so tracing would be on by default; it also adds about 1.6 MB to the binary by linking metric and log exporters too.
- **configger settings for everything** (`tracing.endpoint`, `tracing.protocol`, …): consistent with other settings, but a second, unexpected set of names for options the OTel specification already defines, and more code to keep in step with the exporters.

## Consequences

- `AM_AUTOTASK_TRACING_EXPORTER` and `OTEL_*` variables configure different parts of tracing; the README documents which is which.
- Only OTLP over HTTP (`http/protobuf`) is supported. If `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` or `OTEL_EXPORTER_OTLP_PROTOCOL` is set to anything else, such as `grpc`, the receiver refuses to start rather than send HTTP to a gRPC endpoint. Supporting gRPC as well was rejected for now to keep one protocol to test and document, not for dependency weight: the HTTP exporter already links gRPC through its configuration package and the shared OTLP protocol package, so adding `otlptracegrpc` would cost one module and about 0.2 MB.
- `OTEL_TRACES_EXPORTER` and `OTEL_PROPAGATORS` are not honoured: the exporter comes from `tracing.exporter`, and the propagators are fixed to W3C TraceContext and Baggage to match Alertmanager.
- `OTEL_SDK_DISABLED` is not implemented by the Go SDK; `tracing.exporter: none` is the way to turn tracing off.
