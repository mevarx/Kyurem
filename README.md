# Kyurem

A2A gateway — right protocol, one wire, zero surprises. Kyurem moves the
protocol out of every agent's code path and parks it at the wire's edge.

## Status

**M1 — Transparent proxy (complete).** The gateway forwards A2A traffic
between clients and agents, serves rewritten agent cards, propagates trace
context and ships Prometheus metrics while staying transparent to both
sides.

**M2 — Tracing (complete).** Every gateway handoff is an OpenTelemetry
span, a task is one trace tree across agent hops, and spans carry the
caller/callee, task/context identity, status, latency and byte counters the
dashboard will need. Export is asynchronous and bounded: a collector outage
costs spans, never traffic.

**M3 — Dashboard MVP (in progress).** A read-only management API on its own
listener, a React dashboard (overview, tasks, task detail with a
root-cause hint, agent graph, costs, agents, settings) and cost accounting
for the agents that report token usage. Later milestones (per PRD §10) add
trust and policy, failure handling and production hardening.

**M4 — Hardening and benchmarks (in progress).** Metrics polish (stream-cap
rejection counter, unified card-serving instrument), graceful-shutdown and
connection-draining tests, tiered load tests at 100/1k/5k/10k streams, a
reproducible real-process benchmark script (`demo/scenarios/bench-load.*`)
and a published methodology/results doc (`docs/benchmarks.md`). Heavy tiers
and official `a2a-tck`/`a2a-itk` conformance runs are validated on Linux
CI; this repository's default `go test` runs the smoke tier plus the unit
and integration suites.

The milestone plan, including what is explicitly out of scope, lives in
[`docs/milestones.md`](docs/milestones.md).

## What M1 delivers

| Guarantee | What it means |
|---|---|
| GW-1 | A2A call routing: `/a2a/{agent}` (aliases supported) → upstream |
| GW-2 | Streaming passthrough for SSE (`SendStreamingMessage`) |
| GW-3 | Agent-card rewriting so discovery loops back through the gateway |
| GW-4 | Card fetching/caching with TTL + ETag and stale-on-error grace |
| GW-5 | Trace-context derivation/propagation (`traceparent`/`tracestate`) |
| GW-6 | Upstream timeouts + fail-fast 502 on unreachable agents |
| GW-7 | Graceful shutdown / connection draining on SIGINT/SIGTERM |

## What M2 delivers

| Guarantee | What it means |
|---|---|
| TR-1 | One span per handoff, named `invoke_agent {callee}` (GenAI conventions) |
| TR-2 | Parent/child links reconstruct the call tree of one task |
| TR-3 | Caller, callee, task ID, context ID, status, latency, bytes in/out per span |
| TR-4 | Payload capture stays off (reserved; `payload_capture: true` is rejected) |
| TR-5 | OTLP export with batching, retry, a bounded queue and drop counters |
| TR-6 | The GenAI conventions version is pinned and documented |

The attribute contract, sampling rules, drop semantics and the pinned
semantic-convention commit live in
[`docs/telemetry-conventions.md`](docs/telemetry-conventions.md).

## What M3 delivers

| Piece | What it means |
|---|---|
| Management API | `GET /api/v1/{tasks,tasks/{id},graph,agents,costs,overview,settings}` on `listeners.mgmt.addr`, bearer-authenticated, described by [`api/openapi.yaml`](api/openapi.yaml) |
| Task detail | The span tree of one task, its events, its cost breakdown and a **root-cause hint** (a heuristic, labelled as such) |
| Agent graph | Caller-to-callee edges with call counts, error rate, p95 latency and cost |
| Costs | Token usage read from what the agent reported, priced from a config table, aggregated per agent/task/model/window — unknown stays unknown |
| Dashboard | React + TypeScript + Vite app (`dashboard/`), dark and light themes, generated API types |

## What M4 delivers

| Piece | What it means |
|---|---|
| Metrics polish | `kyurem_proxy_stream_cap_rejections_total` counts SSE responses rejected because `limits.max_concurrent_streams` was full; card cache outcomes collapse into the `kyurem_card_served_total` outcome label (one bounded instrument instead of two) |
| Load tiers | 100/1k/5k/10k concurrent SSE streams through the real proxy handler, with relay-integrity (exactly 6 events/stream), gauge-drain and proxied-request-counter assertions; opt-in via `KYUREM_LOAD_FULL=1k,5k,10k` |
| Reproducible benchmark | `demo/scenarios/bench-load.ps1` / `.sh` build the binaries, boot a real gateway + agent, run every tier and emit Markdown + per-tier JSON; see [`docs/benchmarks.md`](docs/benchmarks.md) |
| Published numbers | ~333 rps flat across tiers (bottleneck is the echo agent's 40 ms inter-chunk sleep, not the gateway); p50 12 ms (100), 119 ms (1k), 320 ms (5k), 597 ms (10k) |

## Quickstart

Requirements: **Go 1.26+**. No Docker needed for the gateway itself or for
the M1 demo; only the M2 tracing stack below needs Docker.

```bash
# build everything
go build -o demo/bin/kyurem      ./gateway/cmd/kyurem
go build -o demo/bin/go-echo     ./demo/agents/go-echo
go build -o demo/bin/go-upper    ./demo/agents/go-upper
go build -o demo/bin/demo-client ./demo/scenarios/client

# boot in order: echo (agent) -> gateway -> upper (chained agent)
./demo/bin/go-echo --port 9010 &
./demo/bin/kyurem serve --config config/kyurem.example.yaml &
./demo/bin/go-upper --port 9011 \
  --echo-card-url http://127.0.0.1:8080/a2a/echo/.well-known/agent-card.json &

# talk to the agents *through the gateway*
./demo/bin/demo-client --gateway-url http://127.0.0.1:8080/a2a/echo --message "hello"
./demo/bin/demo-client --gateway-url http://127.0.0.1:8080/a2a/greeter --message "alias"   # alias
./demo/bin/demo-client --gateway-url http://127.0.0.1:8080/a2a/echo --message "hi" --stream # SSE
./demo/bin/demo-client --gateway-url http://127.0.0.1:8080/a2a/upper --message "hi"        # chain
```

The scripted one-command version runs the same flow with assertions:

```bash
make run-demo                  # bash
pwsh demo/scenarios/m1-basic.ps1  # Windows
```

Health/readiness/metrics/version live on the **admin listener**
(`127.0.0.1:8081` by default): `/healthz`, `/readyz`, `/metrics`,
`/version`. The A2A data path (`:8080`) never exposes them.

## Tracing stack (M2)

With Docker and the compose v2 plugin, the full tracing path comes up in
one command — gateway → OTel collector → ClickHouse, plus Prometheus and
Grafana:

```bash
make demo-m2      # start the stack, send a task, assert one task = one trace tree
make compose-up   # just the stack (demo-client seeds one task)
make compose-down # stop it and drop the volumes
```

Afterwards:

- **Grafana** — <http://localhost:3000> (anonymous admin) → *Explore* →
  `ClickHouse` → **Traces** → open the newest trace: `invoke_agent upper`
  with the nested `invoke_agent echo` underneath it.
- **Prometheus** — <http://localhost:9090> → `kyurem_telemetry_*` series
  (spans ended/exported/dropped, queue depth, export retries).
- **ClickHouse** — <http://localhost:8123> (user `default`, password
  `kyurem-demo`) → `otel.otel_traces`; the trace-tree query is in
  [`docs/telemetry-conventions.md`](docs/telemetry-conventions.md).

Without Docker the gateway still traces: set
`telemetry.otlp_endpoint` to any OTLP collector, or leave it empty and keep
the W3C propagation only.

## The management API and dashboard (M3)

The gateway serves a read-only management API on its own listener
(`listeners.mgmt.addr`, default `127.0.0.1:8082`), authenticated with a
bearer token. The contract is [`api/openapi.yaml`](api/openapi.yaml); the
dashboard's types are generated from it, never hand-written.

```bash
# the API, straight from the gateway (loopback, no token needed)
curl -s http://127.0.0.1:8082/api/v1/tasks | head
curl -s http://127.0.0.1:8082/api/v1/overview | head

# the dashboard (needs Node 20+)
make dashboard-install
make dashboard-check      # typecheck, lint, component tests
make dashboard-build
cd dashboard && npm run dev
```

With the compose stack the dashboard is served by nginx on
<http://localhost:3001> and proxies `/api` to the gateway; the demo token
is `kyurem-demo` (set it in the dashboard's Settings page).

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/overview` | summary cards, time series, top agents, recent failures |
| `GET /api/v1/tasks` | filtered, paginated task list |
| `GET /api/v1/tasks/{id}` | span tree, events, cost breakdown, root-cause hint |
| `GET /api/v1/graph` | agent-to-agent edges with counts, error rate, latency, cost |
| `GET /api/v1/agents` | registry, card status, verification, health, traffic |
| `GET /api/v1/costs` | cost and token roll-ups (by agent, model, task, window) |
| `GET /api/v1/settings` | versions, telemetry counters, storage driver |

Costs are honest: an agent that does not report token usage yields
`null` (unknown), never `0`. The demo echo agent reports usage so the path
is visible end to end.

## The demo agents

Both are ordinary SDK A2A agents — no gateway awareness:

- **go-echo** — replies `echo: <your text>`; streams word-by-word with a
  full task lifecycle (`submitted → working → artifacts → completed`).
- **go-upper** — calls go-echo *through the gateway* and answers
  `upper: <UPPERCASED>`; the echo marker check makes accidental pass-through
  impossible, proving agent-to-agent routing across Kyurem. It relays the
  inbound trace context, so the nested hop becomes a child span of the first
  one, and it runs the same task lifecycle when the call streams.

## Repository layout

```
kyurem/
├── api/openapi.yaml            # management API contract (dashboard types are generated from it)
├── gateway/                    # Go module: the gateway
│   ├── cmd/kyurem/             # serve | validate | version
│   ├── internal/
│   │   ├── config/             # YAML + env loading, validation
│   │   ├── server/             # listeners, health, graceful shutdown
│   │   ├── proxy/              # A2A forwarding, streaming, card rewriting
│   │   ├── registry/           # agent registry, card fetch and cache
│   │   ├── telemetry/          # OTel setup, handoff spans, async OTLP export
│   │   ├── storage/            # span store (memory, clickhouse) + query layer
│   │   ├── cost/               # price table, per-span cost, token usage
│   │   ├── mgmtapi/            # management API handlers (M3)
│   │   └── metrics/            # Prometheus collectors
│   └── test/                   # integration + bench (load) suites
├── dashboard/                  # TypeScript app: React + Vite (M3)
├── demo/                       # Go module: demo agents and the demo client
│   ├── agents/go-echo/         # SDK echo agent (reports token usage)
│   ├── agents/go-upper/        # SDK chained agent (calls echo via the gateway)
│   └── scenarios/              # scripted demos (m1-basic, m2-tracing)
├── config/                     # shipped configurations (example + compose)
├── deploy/                     # Dockerfiles, compose stack, collector, ClickHouse,
│                               # Prometheus, Grafana and nginx provisioning
├── docs/                       # configuration, telemetry conventions, milestones, ADRs
└── .github/workflows/          # CI: build, vet, format, tests, compose + dashboard
```

## Configuration

Single YAML file, validated at startup; scalar leaves are overridable via
`KYUREM_*` environment variables (see `config/kyurem.example.yaml`).
Validate without starting:

```bash
go run ./gateway/cmd/kyurem validate --config config/kyurem.example.yaml
make validate    # both shipped configs
```

The full option reference, including every `telemetry.*` key and its
environment variable, is in [`docs/configuration.md`](docs/configuration.md).

## Development

```bash
make test             # gateway unit + integration tests (race detector)
make test-demo        # demo module tests
make vet
make build-demo       # demo agents + client
make demo-m3          # a scripted failure is diagnosable through the API
make dashboard-check  # dashboard typecheck, lint and component tests
```

The integration suite (`gateway/test/integration`) spins up real SDK demo
agents behind the real proxy handler and drives them with the official
`a2a-go` SDK client — discovery, round trips, SSE, card rewriting, trace
rules, unreachable-upstream 502 and the upper→echo chain. The M2 suite
additionally asserts the trace tree: two handoff spans in one trace with
the right parent, the GenAI/kyurem attributes, task identity on a streamed
task, error marking on a failed handoff and the pinned resource attributes.
The M3 suite drives the same traffic and reads it back through the
management API: the task list, the span tree, the root-cause hint of a
failed task, the agent graph and the "cost unknown" rule.

**Verification status.** The Go path (M1 to M3) is covered by those tests.
The compose stack (`deploy/`) was authored but **not executed** in the
environment where M3 was implemented, because Docker was unavailable there;
CI validates the compose file and both configs statically, and runs the
dashboard's typecheck, lint, tests and build. Run `make demo-m2` on a
Docker host to verify the collector/ClickHouse path end to end.

## Documentation

| Document | Contents |
|---|---|
| [`docs/benchmarks.md`](docs/benchmarks.md) | load-test methodology, tiers, published numbers, threats to validity |
| [`docs/milestones.md`](docs/milestones.md) | what each milestone delivers, how it is verified, what is out of scope |
| [`docs/configuration.md`](docs/configuration.md) | every configuration key, env var and validation rule |
| [`docs/telemetry-conventions.md`](docs/telemetry-conventions.md) | pinned conventions, span/attribute contract, sampling, export and drop semantics, trace queries |
| [`api/openapi.yaml`](api/openapi.yaml) | the management API contract |
| [`docs/adr/`](docs/adr/) | architecture decision records (module layout, routing/cards, trace context, trace export, management API, cost model) |

The product requirements document (`Kyurem Product Requirements Document.md`)
is a local source document and is intentionally not committed.
