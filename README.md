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
costs spans, never traffic. Later milestones (per PRD §10) add durable
storage, the management API/dashboard, trust, failover and production
hardening.

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
├── gateway/                  # Go module: the gateway
│   ├── cmd/kyurem/           # serve | validate | version
│   ├── internal/
│   │   ├── config/           # YAML + env loading, validation
│   │   ├── server/           # listeners, health, graceful shutdown
│   │   ├── proxy/            # A2A forwarding, streaming, card rewriting
│   │   ├── registry/         # agent registry, card fetch and cache
│   │   ├── telemetry/        # OTel setup, handoff spans, async OTLP export
│   │   └── metrics/          # Prometheus collectors
│   └── test/integration/     # in-process gateway + SDK agents
├── demo/                     # Go module: demo agents and the demo client
│   ├── agents/go-echo/       # SDK echo agent
│   ├── agents/go-upper/      # SDK chained agent (calls echo via the gateway)
│   └── scenarios/            # scripted demos (m1-basic, m2-tracing)
├── config/                   # shipped configurations (example + compose)
├── deploy/                   # Dockerfiles, compose stack, collector, ClickHouse,
│                             # Prometheus and Grafana provisioning
├── docs/                     # configuration, telemetry conventions, ADRs
└── .github/workflows/        # CI: build, vet, format, tests, compose validation
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
make test        # gateway unit + integration tests (race detector)
make test-demo   # demo module tests
make vet
make build-demo  # demo agents + client
```

The integration suite (`gateway/test/integration`) spins up real SDK demo
agents behind the real proxy handler and drives them with the official
`a2a-go` SDK client — discovery, round trips, SSE, card rewriting, trace
rules, unreachable-upstream 502 and the upper→echo chain. The M2 suite
additionally asserts the trace tree: two handoff spans in one trace with
the right parent, the GenAI/kyurem attributes, task identity on a streamed
task, error marking on a failed handoff and the pinned resource attributes.

**Verification status.** The M2 Go path is covered by those tests. The
compose stack (`deploy/`) was authored but **not executed** in the
environment where M2 was implemented, because Docker was unavailable there;
CI validates the compose file and both configs statically. Run
`make demo-m2` on a Docker host to verify the collector/ClickHouse path
end to end.

## Documentation

| Document | Contents |
|---|---|
| [`docs/configuration.md`](docs/configuration.md) | every configuration key, env var and validation rule |
| [`docs/telemetry-conventions.md`](docs/telemetry-conventions.md) | pinned conventions, span/attribute contract, sampling, export and drop semantics, trace queries |
| [`docs/adr/`](docs/adr/) | architecture decision records (module layout, routing/cards, trace context, trace export) |

The product requirements document (`Kyurem Product Requirements Document.md`)
is a local source document and is intentionally not committed.
