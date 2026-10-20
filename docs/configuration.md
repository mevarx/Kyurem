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

## Reference (M1 + M2)

| YAML path | Meaning | Default |
|---|---|---|
| `listeners.a2a.addr` | A2A data-path listener (`addr:port`) | `127.0.0.1:8080` |
| `listeners.a2a.public_base_url` | Base URL used for card rewriting; empty = derive from request Host | `""` |
| `listeners.a2a.route_prefix` | Listen prefix under which agents are exposed | `/a2a` |
| `listeners.admin.addr` | Health/readiness/metrics listener | `127.0.0.1:8081` |
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
| `card_cache.ttl` | Freshness window of fetched agent cards | `60s` |
| `card_cache.stale_grace` | Serve stale cards this long when refreshes fail | `10m` |
| `card_cache.max_card_bytes` | Hard cap on a fetched card body (untrusted input) | 256 KiB |
| `limits.max_concurrent_streams` | Cap on live SSE responses per process | `1024` |
| `limits.max_request_body_bytes` | Cap on proxied request bodies (413 beyond) | 64 MiB |
| `limits.shutdown_grace` | Drain deadline on SIGINT/SIGTERM | `30s` |
| `limits.timeouts.connect` | Upstream TCP dial timeout | `5s` |
| `limits.timeouts.response_header` | Wait for the upstream's first response byte | `30s` |
| `limits.timeouts.request` | Total per-request deadline incl. streams; `0s` = none | `0s` |

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
  credentials** (secrets never ride config URLs — use headers/env in later
  trust milestones);
- URLs normalize without trailing slashes; duplicate aliases are rejected.
