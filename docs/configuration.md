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

## Reference (M1)

| YAML path | Meaning | Default |
|---|---|---|
| `listeners.a2a.addr` | A2A data-path listener (`addr:port`) | `127.0.0.1:8080` |
| `listeners.a2a.public_base_url` | Base URL used for card rewriting; empty = derive from request Host | `""` |
| `listeners.a2a.route_prefix` | Listen prefix under which agents are exposed | `/a2a` |
| `listeners.admin.addr` | Health/readiness/metrics listener | `127.0.0.1:8081` |
| `logging.level` | `debug` \| `info` \| `warn` \| `error` | `info` |
| `logging.format` | `text` \| `json` | `json` |
| `telemetry.otlp_endpoint` | OTLP exporter consumed from M2; empty = off | `""` |
| `telemetry.payload_capture` | Reserved for TR-4 (off by default) | `false` |
| `card_cache.ttl` | Freshness window of fetched agent cards | `60s` |
| `card_cache.stale_grace` | Serve stale cards this long when refreshes fail | `10m` |
| `card_cache.max_card_bytes` | Hard cap on a fetched card body (untrusted input) | 256 KiB |
| `limits.max_concurrent_streams` | Cap on live SSE responses per process | `1024` |
| `limits.max_request_body_bytes` | Cap on proxied request bodies (413 beyond) | 64 MiB |
| `limits.shutdown_grace` | Drain deadline on SIGINT/SIGTERM | `30s` |
| `limits.timeouts.connect` | Upstream TCP dial timeout | `5s` |
| `limits.timeouts.response_header` | Wait for the upstream's first response byte | `30s` |
| `limits.timeouts.request` | Total per-request deadline incl. streams; `0s` = none | `0s` |

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
