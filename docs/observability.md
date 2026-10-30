# Observability

## Metrics

CAPNICo exposes NICo API request metrics through the controller manager's
Prometheus endpoint. Use these metrics to inspect request volume, HTTP
responses, and request latency.

### Metrics Endpoint

The manager serves `/metrics` over HTTP on port 8080 by default. The
`--metrics-bind-address` flag controls its listen address and defaults to
`:8080`.

The default Kustomize installation and Helm chart expose the endpoint through
the `controller-manager-metrics-service` Service. You can inspect the endpoint
by forwarding the Service port from your management cluster. Replace the
namespace if your installation uses another namespace.

```bash
kubectl -n capnico-system port-forward service/controller-manager-metrics-service 8080:8080
```

With the forwarding command running, open
[the local metrics endpoint](http://localhost:8080/metrics).

If you use Prometheus Operator, enable the `../prometheus` resource in
`config/default/kustomization.yaml` before applying your Kustomize installation.
For the Helm chart's default HTTP metrics configuration, set
`prometheus.enabled=true` to render the ServiceMonitor. Both installations
disable the ServiceMonitor by default. Configure your Prometheus instance to
select the ServiceMonitor and its namespace.

### NICo API Requests

CAPNICo registers the following metrics in the manager's metrics registry.

| Metric | Type | Meaning |
|---|---|---|
| `capnico_api_requests_total` | Counter | Number of NICo SDK calls, grouped by operation and HTTP status code. |
| `capnico_api_request_duration_seconds` | Histogram | Duration of NICo SDK calls in seconds, including response reading and decoding. |

Both metrics have the following labels.

| Label | Values |
|---|---|
| `operation` | One of the operation names in the table below. |
| `status_code` | HTTP status code as a string, such as `"200"`, `"201"`, or `"503"`; `"none"` when the SDK returns no HTTP response. |

Labels contain no object IDs, URLs, or error text. A series appears after its
first observed call. Metrics reset when the manager process restarts.

Each SDK call produces one observation. Paginated site and machine listings
produce one observation per requested page. Authentication or configuration
failures before the SDK call produce no observation. Request duration excludes
the authentication setup that precedes the SDK call.

A connectivity failure while the controller reads an already provisioned
instance does not change the `MachineProvisioned` condition. Those failures
appear as the `get_instance` operation with `status_code="none"`.

The HTTP status label records the response code even when SDK decoding later
fails. For example, a response with status `200` and an unsupported enum value
still increments the `"200"` series.

The histogram uses Prometheus default bucket boundaries of `0.005`, `0.01`,
`0.025`, `0.05`, `0.1`, `0.25`, `0.5`, `1`, `2.5`, `5`, and `10` seconds,
plus the `+Inf` bucket. It exports `_bucket`, `_sum`, and `_count` series.
Instance lifecycle durations are not currently exported.

The operation label identifies the SDK API operation.

| Operation | Request |
|---|---|
| `get_current_tenant` | Discover the current tenant. |
| `get_instance_type` | Read an instance type with allocation statistics. |
| `create_instance` | Create an instance. |
| `delete_instance` | Request instance deletion. |
| `update_instance` | Update instance labels or request a hard reboot. |
| `machine_power_control` | Request a graceful machine restart. |
| `get_instance` | Read an instance. |
| `get_machine` | Read a machine with metadata. |
| `list_sites` | List sites while resolving a site. |
| `get_vpc` | Read a VPC. |
| `list_instances` | Look up instances by name. |
| `list_machines` | List machines for failure-domain discovery. |

### Example Queries

Use the following query to calculate requests per second over a 5-minute
window, grouped by operation and HTTP status.

```promql
sum by (operation, status_code) (
  rate(capnico_api_requests_total[5m])
)
```

Use the following query to estimate the 95th percentile request duration per
operation, aggregated across HTTP statuses and manager replicas.

```promql
histogram_quantile(0.95,
  sum by (operation, le) (
    rate(capnico_api_request_duration_seconds_bucket[5m])
  )
)
```

## Tracing

CAPNICo exports OpenTelemetry Protocol (OTLP) traces over gRPC when you set
`OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`.
The traces-specific endpoint takes precedence. Without either variable,
CAPNICo does not configure a trace exporter.

For Helm installations, use the existing environment overrides. This example
samples 10% of new traces and follows the sampling decision of parent spans.

```yaml
manager:
  envOverrides:
    OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector.observability.svc.cluster.local:4317
    OTEL_TRACES_SAMPLER: parentbased_traceidratio
    OTEL_TRACES_SAMPLER_ARG: "0.1"
```

`OTEL_SERVICE_NAME` defaults to `capnico-controller`. Sampling uses the
OpenTelemetry SDK's `OTEL_TRACES_SAMPLER` and `OTEL_TRACES_SAMPLER_ARG`
settings. The default sampler is `parentbased_always_on`.

Each reconciliation creates a span with child spans for lifecycle stages and
NICo HTTP requests. HTTP spans include response-body reading. NICo continues
the trace when its own tracing is enabled.

Requeues create separate reconciliation spans. Correlate them by resource
namespace and name. Logs include `traceID` and `spanID` for correlation with
the current reconciliation. Expected waits use `capnico.outcome=waiting`
and `capnico.wait_reason` without marking the stage as an error.
