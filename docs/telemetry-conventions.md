# Telemetry conventions (M2)

Kyurem's M2 milestone turns one A2A task into **one trace tree**: every
gateway handoff is a span, and nested agent calls are children of the hop
that invoked them. This document is the contract for span names, attributes,
propagation, sampling, export behavior and the pinned semantic-convention
version. TR-4 (payload capture) is deliberately not part of M2.

Implementation: `gateway/internal/telemetry`, wired from
`gateway/internal/proxy/forward.go`.

## Pinned versions

| Component | Pin | Notes |
|---|---|---|
| OpenTelemetry Go API/SDK | `go.opentelemetry.io/otel* v1.47.0` | SDK, trace API, OTLP HTTP/gRPC exporters |
| OTLP protobuf | `go.opentelemetry.io/proto/otlp v1.11.0` | transitively pinned by the exporters |
| Core semantic conventions | `v1.44.0` | HTTP and resource attributes (`service.name`, `service.version`) |
| GenAI semantic conventions | `open-telemetry/semantic-conventions-genai` @ `547934afa90a34298441caf80c43b27255c3a292` | agent spans and `gen_ai.*` attributes |

The GenAI conventions have no stable tagged release for the agent spans used
here, so the repository commit is the pin. Stability is recorded as
`development`. The pins are attached to **every exported span** as resource
attributes, which makes a mixed-version fleet diagnosable in the backend:

| Resource attribute | Value |
|---|---|
| `kyurem.genai.semconv.repo` | `https://github.com/open-telemetry/semantic-conventions-genai` |
| `kyurem.genai.semconv.commit` | `547934afa90a34298441caf80c43b27255c3a292` |
| `kyurem.genai.semconv.stability` | `development` |
| `kyurem.otel.semconv.version` | `1.44.0` |
| `service.name` | `telemetry.service_name` (default `kyurem-gateway`) |
| `service.version` | gateway build version |

### Upgrading a pin

1. Bump the dependency/commit and re-read the GenAI agent-span specification.
2. Update the constants in `gateway/internal/telemetry/attrs.go` and this
   table. The commit constant is the single source of truth; the exporter
   tests assert the resource attributes.
3. Run the M2 integration suite. Span names or attribute names that changed
   upstream must be treated as a breaking telemetry change (dashboards and
   saved queries reference them).
4. Do not mix conventions silently: a deployment's resource attributes say
   which pin produced its spans.

## Trace model

- **One span per gateway handoff** (TR-1). A request proxied from the gateway
  to an upstream agent produces exactly one span. Card requests, health
  probes and the admin listener are not traced.
- **Span name**: `invoke_agent {callee}`, for example `invoke_agent echo`.
- **Span kind**: `SERVER`. The gateway is receiving the A2A call on behalf of
  the callee.
- **Parent/child** (TR-2): the span's parent is the caller's W3C context when
  present. For `client -> gateway -> upper -> gateway -> echo`, the two
  gateway spans share a trace and the echo span's parent is the upper span.
  A client that sends no context gets a gateway-rooted trace.

### W3C propagation

The gateway uses the OpenTelemetry `TraceContext` propagator (replacing the
manual M1 derivation; see ADR 0003 and ADR 0004):

| Inbound `traceparent` | Result |
|---|---|
| Valid and sampled | same trace ID, new span ID, inbound flags and `tracestate` preserved |
| Valid and not sampled | same trace ID, new span ID; `ParentBased` keeps the caller's drop decision |
| Absent or malformed | fresh gateway root, sampled according to `telemetry.sample_ratio` |

The outbound request always carries a fresh `traceparent` for the gateway
span plus the caller's `tracestate`. Baggage is not propagated in M2. A
telemetry failure never changes an HTTP status: propagation and export are
fail-open, and the data path never waits for either.

### Caller attribution

`kyurem.caller.agent` is resolved in this order:

1. **Tracked parent.** When this gateway process emitted the parent span, a
   bounded in-memory tracker (4096 entries) maps `(trace ID, span ID)` to the
   callee name and hop. The nested handoff is attributed to that agent and
   its hop index is incremented.
2. **`X-Kyurem-Caller` hint.** Optional header used by agents that cannot
   propagate trace context. It is untrusted attribution only, sanitized to a
   printable 128-character token.
3. **`unknown`.** No attribution is available; `kyurem.hop.index` stays `0`.

The tracker is process-local, so caller inference degrades to the hint (or
`unknown`) when a nested call lands on another gateway replica. M2 does not
change the A2A wire format to carry caller identity.

## Span attributes

Attributes marked *when observed* are omitted rather than emitted as empty
strings. Task and context IDs are extracted from at most 64 KiB of the
request and response prefixes; response values win, request values fill gaps
(for example a streaming response whose IDs preceded the captured prefix).

### GenAI conventions

| Attribute | Value / source |
|---|---|
| `gen_ai.operation.name` | always `invoke_agent` |
| `gen_ai.provider.name` | always `kyurem` |
| `gen_ai.agent.name` | callee name from the registry |
| `gen_ai.conversation.id` | A2A `contextId`, when observed |
| `error.type` | failure class, only on an error span (see status mapping) |

`gen_ai.provider.name=kyurem` is an intentional custom provider value: the
gateway, not a model vendor, owns the hop. Token usage is not available in
M2, so no `gen_ai.usage.*` attributes are invented.

### Kyurem attributes (PRD §6.1)

| Attribute | Type | Meaning |
|---|---|---|
| `kyurem.task.id` | string | A2A task ID, when observed |
| `kyurem.context.id` | string | A2A context ID, when observed |
| `kyurem.caller.agent` | string | Resolved caller, or `unknown` |
| `kyurem.callee.agent` | string | Registry name of the upstream agent |
| `kyurem.hop.index` | int | `0` for an unattributed root, incremented per nested gateway hop |
| `kyurem.card.verified` | string | `unchecked` in M2 (signature verification is M5) |
| `kyurem.tokens.known` | bool | `true` when the agent reported token usage, `false` otherwise |
| `kyurem.bytes.in` | int64 | request body bytes read from the client |
| `kyurem.bytes.out` | int64 | response body bytes written to the client |
| `kyurem.latency.ms` | float64 | wall-clock handoff duration in milliseconds |
| `kyurem.task.status` | string | latest A2A task state observed in the response/SSE stream |
| `kyurem.cost.usd` | float64 | computed cost of the span (M3, CO-2); absent when unknown |

`kyurem.task.status` keeps the **latest** state, not the first one: a
streamed lifecycle ends as `TASK_STATE_COMPLETED`, not `TASK_STATE_SUBMITTED`.
Plain JSON, JSON-RPC, SSE `data:` frames and truncated prefixes are supported;
the extraction is tolerant but never retains more than the bounded prefix.

### Token usage and cost (M3, CO-1/CO-2)

| Attribute | Type | Meaning |
|---|---|---|
| `gen_ai.usage.input_tokens` | int64 | input tokens the agent reported |
| `gen_ai.usage.output_tokens` | int64 | output tokens the agent reported |
| `gen_ai.request.model` | string | the model the agent reported |

The gateway reads the usage an agent reports (`metadata.usage` with
`input_tokens`/`output_tokens` or `prompt_tokens`/`completion_tokens`, and a
`model` string) from the bounded response prefix. When the agent reports
nothing, the span keeps `kyurem.tokens.known=false` and carries **no** usage
attributes: unknown is never rendered as zero.

The cost is computed when the span ends: the price table (`pricing:` in the
config) is matched against the reported model, and `kyurem.cost.usd` is
written only when both the usage and a price are known. A model without a
price entry leaves the cost unknown — the attribute is simply absent. See
[ADR 0006](adr/0006-token-usage-and-cost-model.md).

### HTTP attributes

| Attribute | Meaning |
|---|---|
| `http.request.method` | inbound method (`POST`, `GET`, ...) |
| `url.path` | inbound A2A path, before upstream rewriting |
| `http.response.status_code` | status written to the caller (when one was written) |

`kyurem.bytes.*` counts body bytes only; HTTP headers are not included.

## Span status and errors

Successful handoffs keep status `Unset` (the OTel default); they do not set
`Ok`, so an unset status is not ambiguous with a failure.

| Condition | Attributes | Span status |
|---|---|---|
| Proxied response < 500 and task not failed | HTTP/byte/latency/task attrs | `Unset` |
| Proxy failure before a response (unreachable, timeout, cap, oversized body) | `error.type` = `upstream_error`, `upstream_timeout`, `client_canceled`, `stream_cap_exhausted`, `body_too_large`, `no_upstream`, `bad_upstream_url`, ...; recorded error event | `Error` |
| Upstream response ≥ 500 | `error.type=http_5xx` | `Error` |
| A2A task state contains `failed`, `canceled`, `cancelled` or `rejected` | `error.type=task_failed` | `Error` |

A 4xx that the upstream itself produced is left as a normal handoff unless
the A2A task state says it failed. This keeps client errors distinguishable
from gateway/agent failures.

## Export pipeline (TR-5)

A real SDK `TracerProvider` is always created, even when export is disabled,
so propagation and span creation behave identically in tests and production.
With an empty `otlp_endpoint`, only the exporter is omitted.

The SDK's `BatchSpanProcessor` is replaced by a small bounded asynchronous
processor (`gateway/internal/telemetry/processor.go`) so every dropped span
and retry is visible. Its behavior:

1. `OnEnd` never blocks: it appends to `telemetry.queue_size`. A full queue
   drops the span immediately and increments a reason-labeled counter.
2. A background goroutine exports batches of at most `batch_size` spans, or a
   partial batch after `batch_timeout`.
3. Each attempt is bounded by `export_timeout`; failures retry up to
   `max_retries` times with exponential backoff from `initial_backoff` to
   `max_backoff`.
4. Shutdown stops admission, drains the queue, and waits up to 10 seconds
   (the server's telemetry shutdown budget) before abandoning the remainder.
5. A span is dropped only after it is counted. A telemetry outage therefore
   costs spans, never requests.

Drop reasons: `queue_full`, `export_failed`, `shutdown`.

| Metric | Meaning |
|---|---|
| `kyurem_telemetry_spans_ended_total` | spans admitted to the export pipeline |
| `kyurem_telemetry_spans_exported_total` | spans accepted by the collector |
| `kyurem_telemetry_spans_dropped_total{reason}` | spans discarded, by reason |
| `kyurem_telemetry_export_failures_total` | batches that exhausted retries |
| `kyurem_telemetry_export_retries_total` | failed attempts that were retried |
| `kyurem_telemetry_export_duration_seconds` | per-attempt export latency |
| `kyurem_telemetry_queue_depth` / `_capacity` | current and configured queue bounds |

Sampling is head-based: `ParentBased(TraceIDRatioBased(sample_ratio))`.
Inbound sampled traces are always kept, even when the local ratio is lower.

## Reading a trace

The compose stack (`deploy/compose/docker-compose.yml`) runs gateway →
collector → ClickHouse, plus Prometheus and Grafana. A fresh `up` sends one
chained `upper -> echo` task through the gateway. The collector creates
`otel.otel_traces` and applies a seven-day TTL.

ClickHouse trace tree for the newest task (using the default password from
the compose file):

```sql
SELECT
    TraceId,
    SpanId,
    ParentSpanId,
    SpanName,
    SpanAttributes['kyurem.caller.agent'] AS caller,
    SpanAttributes['kyurem.callee.agent'] AS callee,
    SpanAttributes['kyurem.task.id']      AS task_id,
    SpanAttributes['kyurem.context.id']   AS context_id,
    SpanAttributes['kyurem.task.status']  AS task_status,
    StatusCode,
    Duration / 1e6                        AS latency_ms
FROM otel.otel_traces
WHERE TraceId = (
    SELECT TraceId
    FROM otel.otel_traces
    ORDER BY Timestamp DESC
    LIMIT 1
)
ORDER BY Timestamp ASC;
```

Expected: two rows, one `invoke_agent upper` root and one
`invoke_agent echo` child, with the same `TraceId` and the echo span's
`ParentSpanId` equal to the upper span's `SpanId`. The root hop carries the
task id and the final task status; both hops carry the same A2A context id.
(The nested hop answers with a plain message, so it has no task id of its
own — a nested call starts a new task in the same conversation.)

Grafana (anonymous admin, <http://localhost:3000>) provisions the
`ClickHouse` datasource in traces mode (`otelEnabled: true`). In **Explore**
choose `ClickHouse`, switch to **Traces**, search the latest trace and open
the trace view: the span tree is rendered from `TraceId`/`ParentSpanId`.
Prometheus (<http://localhost:9090>) holds the gateway metrics above; scrape
targets and the export-health series are the first place to look if Grafana
shows no traces.

Run `bash demo/scenarios/m2-tracing.sh` for the scripted version. It waits
for the stack, sends a task, asserts that ClickHouse has at least two spans
in one trace with non-empty task/context attributes, and prints the tree.

The compose stack is a local demo: it publishes loopback ports and uses a
demo ClickHouse password. It was **not executed in the development
environment used to implement M2** (Docker was unavailable); correctness of
the Go path is covered by the in-process M2 integration tests, and the
compose/collector configuration is validated statically in CI.

## Non-goals in M2

- **TR-4 payload capture** is not implemented. `telemetry.payload_capture:
  true` is rejected at startup. The gateway retains only a bounded prefix in
  memory to extract IDs, status and reported usage, and discards it when
  the span ends.
- **Card verification** is `unchecked` until M5.
- **Baggage** is not propagated; only `traceparent`/`tracestate` are.
- **Exemplars, logs and metrics over OTLP** are not part of the M2 trace
  pipeline. Gateway metrics stay on the Prometheus admin endpoint.
- **Budget alerts** (CO-4) arrive with a later milestone.

## Management API and cost (M3)

M3 adds a read-only management API on its own listener
(`listeners.mgmt.addr`) and the dashboard on top of it. The API reads the
same spans this document describes, from ClickHouse (the compose stack) or
from a bounded in-process ring (local development), and the aggregation
semantics live in one place: a task is a trace, the cost of a task is the
sum of the known span costs, and a group without a known cost is reported
as unknown, never as zero. See
[ADR 0005](adr/0005-management-api-and-storage-drivers.md) and
[ADR 0006](adr/0006-token-usage-and-cost-model.md).
