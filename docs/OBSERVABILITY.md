# Observability (OpenTelemetry)

The `collaborate serve` broker exports OpenTelemetry traces and metrics over OTLP/HTTP when
`OTEL_EXPORTER_OTLP_ENDPOINT` is set. With no endpoint configured, telemetry is fully disabled
and the broker behaves exactly as before.

Every broker HTTP request becomes a span named `METHOD /path`, and the standard
`http.server.*` duration and size metrics are recorded. W3C trace context is propagated, so
agents that send a `traceparent` header join the same trace.

## Grafana Cloud (direct)

```sh
export OTEL_SERVICE_NAME="DeontewattsV1"
export OTEL_RESOURCE_ATTRIBUTES="service.namespace=my-application-group,deployment.environment=production"
export OTEL_EXPORTER_OTLP_ENDPOINT="https://otlp-gateway-prod-us-west-0.grafana.net/otlp"
export OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf"
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic%20<base64 of INSTANCE_ID:TOKEN>"
./collaborate serve --db collaborate.sqlite --gates-file gates.json
```

Keep the token out of the repository; load it from an environment file or secret store.

## Through a local Grafana Alloy collector

If Alloy is running with an `otelcol.receiver.otlp` on the default ports, point the broker at it
and leave authentication to Alloy:

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:4318"
export OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf"
```

## Python harness

The optional Python scripts in `scripts/` can be traced with the standard distro:

```sh
pip install opentelemetry-distro opentelemetry-exporter-otlp
opentelemetry-bootstrap --action=install
opentelemetry-instrument python3 scripts/check.sh --quick
```
