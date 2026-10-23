# Kyurem Gateway Configuration

One YAML file per deployment, validated fully at startup (`kyurem validate`
reuses the same loader). Unknown keys are rejected to catch typos early.
Every **scalar** option can be overridden from the environment with a
`KYUREM_` prefix built from the nested `env` tags, e.g.
`KYUREM_LISTENERS_A2A_ADDR=":8080"`, `KYUREM_CARD_CACHE_TTL=90s`,
`KYUREM_LIMITS_TIMEOUTS_CONNECT=3s`. Duration values accept `"60s"`-style
strings; bare numbers mean seconds (both in YAML and env).

Structures that are lists — `agents` — are file-only: they cannot be
overridden by environment variables.

## Reference (M1–M7)

| YAML path | Meaning | Default |
|---|---|---|
| `listeners.a2a.addr` | A2A data-path listener (`addr:port`) | `127.0.0.1:8080` |
| `listeners.a2a.public_base_url` | Base URL used for card rewriting; empty = derive from request Host | `""` |
| `listeners.a2a.route_prefix` | Listen prefix under which agents are exposed | `/a2a` |
| `listeners.admin.addr` | Health/readiness/metrics listener | `127.0.0.1:8081` |
| `listeners.mgmt.addr` | Management API listener (M3), separate from the data path | `127.0.0.1:8082` |
| `logging.level` | `debug` \| `info` \| `warn` \| `error` | `info` |
| `logging.format` | `text` \| `json` | `json` |
| `telemetry.service_name` | `service.name` resource attribute | `kyurem-gateway` |
| `telemetry.otlp_endpoint` | OTLP collector endpoint; empty = export off (propagation stays on) | `""` |
| `telemetry.otlp_protocol` | `http/protobuf` \| `grpc` | `http/protobuf` |
| `telemetry.otlp_insecure` | Disable TLS for `host:port` endpoints (URL endpoints follow their scheme) | `false` |
| `telemetry.sample_ratio` | Head-sampling probability for gateway-rooted traces; `0` = sample nothing at the root | `1` |
| `telemetry.queue_size` | Bounded span export queue; overflow drops spans (never requests) | `2048` |
| `telemetry.batch_size` | Maximum spans per OTLP request; must be ≤ `queue_size` | `512` |
| `telemetry.batch_timeout` | Flush a partial batch after this delay | `5s` |
| `telemetry.export_timeout` | Deadline for one OTLP export attempt | `10s` |
| `telemetry.max_retries` | Retries after a failed export attempt | `4` |
| `telemetry.initial_backoff` | First retry delay (exponential) | `500ms` |
| `telemetry.max_backoff` | Upper bound of the retry delay | `10s` |
| `telemetry.payload_capture` | Reserved for TR-4; `true` is rejected at startup | `false` |
| `management.enabled` | Serve the management API on `listeners.mgmt.addr` | `true` |
| `management.auth_token` | Bearer token the API requires; empty = open (loopback only) | `""` |
| `management.storage.driver` | `memory` (in-process ring) \| `clickhouse` | `memory` |
| `management.storage.memory_capacity` | Spans kept in the in-process ring | `4096` |
| `management.storage.clickhouse.url` | ClickHouse HTTP endpoint (`http://host:8123`) | `""` |
| `management.storage.clickhouse.database` | Database holding the spans | `otel` |
| `management.storage.clickhouse.username` | ClickHouse user | `""` |
| `management.storage.clickhouse.password` | ClickHouse password | `""` |
| `management.storage.clickhouse.timeout` | Deadline of one query | `10s` |
| `pricing[].model` | Model name the prices apply to (CO-2) | — |
| `pricing[].input_per_1k_usd` | Price per 1000 input tokens | — |
| `pricing[].output_per_1k_usd` | Price per 1000 output tokens | — |
| `card_cache.ttl` | Freshness window of fetched agent cards | `60s` |
| `card_cache.stale_grace` | Serve stale cards this long when refreshes fail | `10m` |
| `card_cache.max_card_bytes` | Hard cap on a fetched card body (untrusted input) | 256 KiB |
| `limits.max_concurrent_streams` | Cap on live SSE responses per process | `1024` |
| `limits.max_request_body_bytes` | Cap on proxied request bodies (413 beyond) | 64 MiB |
| `limits.shutdown_grace` | Drain deadline on SIGINT/SIGTERM | `30s` |
| `limits.timeouts.connect` | Upstream TCP dial timeout | `5s` |
| `limits.timeouts.response_header` | Wait for the upstream's first response byte | `30s` |
| `limits.timeouts.request` | Total per-request deadline incl. streams; `0s` = none | `0s` |
| `failure_handling.max_attempts` | Total requests per eligible call (including the first) | `2` |
| `failure_handling.retry_budget.max_retries` | Additional attempts per agent and fixed window | `10` |
| `failure_handling.retry_budget.window` | Fixed retry-budget window | `1m` |
| `failure_handling.circuit_breaker.failure_threshold` | Consecutive upstream failures before opening | `5` |
| `failure_handling.circuit_breaker.open_duration` | Time before one half-open probe | `30s` |
| `failure_handling.max_retry_body_bytes` | Maximum keyed POST body buffered for replay | `65536` |

## Telemetry (M2)

Trace-context propagation always runs. Export is optional: an empty
`telemetry.otlp_endpoint` keeps a real tracer provider (so spans and W3C
propagation behave identically) but installs no exporter.

Endpoint forms:

- `http/protobuf`: `host:port` (use `otlp_insecure: true` for plaintext) or
  an `http(s)://host:port[/path]` URL. A URL without a path targets the
  canonical `/v1/traces`; an explicit path is honored.
- `grpc`: `host:port` only (URLs are rejected for gRPC).

Every scalar telemetry option is overridable from the environment:

| Environment variable | Overrides |
|---|---|
| `KYUREM_TELEMETRY_SERVICE_NAME` | `telemetry.service_name` |
| `KYUREM_TELEMETRY_OTLP_ENDPOINT` | `telemetry.otlp_endpoint` |
| `KYUREM_TELEMETRY_OTLP_PROTOCOL` | `telemetry.otlp_protocol` |
| `KYUREM_TELEMETRY_OTLP_INSECURE` | `telemetry.otlp_insecure` |
| `KYUREM_TELEMETRY_SAMPLE_RATIO` | `telemetry.sample_ratio` |
| `KYUREM_TELEMETRY_QUEUE_SIZE` | `telemetry.queue_size` |
| `KYUREM_TELEMETRY_BATCH_SIZE` | `telemetry.batch_size` |
| `KYUREM_TELEMETRY_BATCH_TIMEOUT` | `telemetry.batch_timeout` |
| `KYUREM_TELEMETRY_EXPORT_TIMEOUT` | `telemetry.export_timeout` |
| `KYUREM_TELEMETRY_MAX_RETRIES` | `telemetry.max_retries` |
| `KYUREM_TELEMETRY_INITIAL_BACKOFF` | `telemetry.initial_backoff` |
| `KYUREM_TELEMETRY_MAX_BACKOFF` | `telemetry.max_backoff` |
| `KYUREM_TELEMETRY_PAYLOAD_CAPTURE` | `telemetry.payload_capture` |

Validation rules (see `gateway/internal/config/validate.go`):

- `otlp_protocol` is `http/protobuf` or `grpc`;
- `otlp_endpoint` must not contain credentials; gRPC endpoints are
  `host:port` without a scheme;
- `sample_ratio` is between `0` and `1` (an explicit `0` is valid and
  distinct from "unset");
- `queue_size` is positive and at most 1048576; `batch_size` is positive and
  at most `queue_size`;
- `batch_timeout` and `export_timeout` are positive;
- `max_retries` is between `0` and `10`; `initial_backoff` and
  `max_backoff` are positive and `max_backoff >= initial_backoff`;
- `payload_capture: true` fails validation (TR-4 is not implemented in M2).

Span names, attributes, sampling and drop semantics are specified in
`docs/telemetry-conventions.md`.

## Management API and pricing (M3)

The management API is a read-only, authenticated, versioned HTTP API under
`/api/v1` on its own listener. It backs the dashboard and is described by
`api/openapi.yaml`.

```yaml
listeners:
  mgmt:
    addr: "127.0.0.1:8082"

management:
  enabled: true
  # Bearer token: "Authorization: Bearer <token>". Empty means open, which
  # validation only allows on a loopback address.
  auth_token: ""
  storage:
    # memory: bounded in-process ring of recent spans (no dependencies).
    # clickhouse: the store the OTel collector writes to.
    driver: memory
    memory_capacity: 4096
    clickhouse:
      url: "http://clickhouse:8123"
      database: otel
      username: default
      password: ""
      timeout: 10s

# Price table (CO-2). The key matches the model an agent reports
# (gen_ai.request.model). An agent that reports no model has no computable
# cost, which the dashboard shows as unknown rather than as zero.
pricing:
  - model: "demo-echo"
    input_per_1k_usd: 0.001
    output_per_1k_usd: 0.002
```

Environment overrides:

| Environment variable | Overrides |
|---|---|
| `KYUREM_LISTENERS_MGMT_ADDR` | `listeners.mgmt.addr` |
| `KYUREM_MANAGEMENT_ENABLED` | `management.enabled` |
| `KYUREM_MANAGEMENT_AUTH_TOKEN` | `management.auth_token` |
| `KYUREM_MANAGEMENT_STORAGE_DRIVER` | `management.storage.driver` |
| `KYUREM_MANAGEMENT_STORAGE_MEMORY_CAPACITY` | `management.storage.memory_capacity` |
| `KYUREM_MANAGEMENT_STORAGE_CLICKHOUSE_URL` | `management.storage.clickhouse.url` |
| `KYUREM_MANAGEMENT_STORAGE_CLICKHOUSE_DATABASE` | `management.storage.clickhouse.database` |
| `KYUREM_MANAGEMENT_STORAGE_CLICKHOUSE_USERNAME` | `management.storage.clickhouse.username` |
| `KYUREM_MANAGEMENT_STORAGE_CLICKHOUSE_PASSWORD` | `management.storage.clickhouse.password` |
| `KYUREM_MANAGEMENT_STORAGE_CLICKHOUSE_TIMEOUT` | `management.storage.clickhouse.timeout` |

`pricing` is a list and therefore file-only.

Validation rules:

- `management.storage.driver` is `memory` or `clickhouse`;
- `memory_capacity` is positive and at most 1048576;
- the clickhouse driver requires `clickhouse.url` with a host;
- `management.auth_token` is required when the management listener is not
  loopback;
- every `pricing` entry has a model and non-negative prices.

## Agents

Each agent is registered by `name` and reached at
`{listeners.a2a.route_prefix}/{name}` (or a custom `path`):

```yaml
agents:
  - name: echo                 # required, unique
    aliases: [greeter]         # optional, extra case-insensitive keys
    path: /custom/echo         # optional full listen-path override
    tags: [demo]               # optional, used by the M3 dashboard
    upstreams:                 # required, priority order
      - url: http://127.0.0.1:9010
        card_url: ""           # optional override for card fetching
        binding: JSONRPC       # optional hint: JSONRPC | HTTP+JSON | GRPC
```

Rules enforced at startup (see `gateway/internal/config/validate.go`):

- `name` is unique and path-valid; `path`, when set, is unique and can
  collide with no generated route (aliases included);
- `upstreams[].url` must be an absolute `http(s)` URL **without
  credentials** (secrets never ride config URLs);
- URLs normalize without trailing slashes; duplicate aliases are rejected.

## Trust and policy (M5)

Caller authentication uses `callers.mode` (`off`, `optional`, `required`) and
maps bearer tokens to caller identities; TLS client certificates may also
identify callers. Store bearer values in environment variables rather than
committing them. Agent-card trust modes (`trust.mode`, optionally overridden
per agent) are `off`, `warn`, or `enforce`; trusted public keys must come from
operator configuration. The gateway does not resolve a signer-provided key
URL.

Policy supports `off`, `warn`, and `enforce`, a default `allow`/`deny`, and
deny-overrides caller/callee rules. Standard A2A requests do not expose an
authoritative selected skill. `X-Kyurem-Skill` is a caller hint, so rules with
`skills` are accepted only in `warn` mode and rejected at startup in
`enforce` mode. The sample profile leaves caller auth, trust, and policy off
for local development; configure these explicitly before exposing the
gateway.

## Failure handling (M6)

Upstreams are ordered by priority within the selected binding. When no
upstream has a matching binding, routing preserves the first-upstream
fallback. `max_attempts` is 1–5 and includes the original request;
`retry_budget.max_retries` is 0–1000 per agent per fixed window. The circuit
breaker is maintained per configured upstream, opens after its configured
consecutive-failure threshold, and admits one probe after `open_duration`.
All durations must be positive and retry-body buffering cannot exceed
`limits.max_request_body_bytes`.

Only bodyless GET/HEAD/OPTIONS and keyed POST requests are replayable. A keyed
POST is buffered only when its known body length is within
`max_retry_body_bytes`; the receiving upstream must honor the
`Idempotency-Key` and deduplicate side effects. Unknown-length, oversized,
unkeyed, and other non-replayable requests are sent once. The gateway retries
network errors and HTTP 502/503/504; other 5xx responses still count toward
breaker state. Retry and failover events are attached to the handoff span.
