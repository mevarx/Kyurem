# Kyurem

A2A gateway — right protocol, one wire, zero surprises. Kyurem moves the
protocol out of every agent's code path and parks it at the wire's edge.

## Status

**M1 — Transparent proxy (complete).** The gateway forwards A2A traffic
between clients and agents, serves rewritten agent cards, propagates trace
context and ships Prometheus metrics while staying transparent to both
sides. Later milestones (per PRD §10) add durable storage, the management
API/dashboard, trust, failover and production hardening.

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

## Quickstart

Requirements: **Go 1.26+**. No Docker needed for M1.

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

## The demo agents

Both are ordinary SDK A2A agents — no gateway awareness:

- **go-echo** — replies `echo: <your text>`; streams word-by-word with a
  full task lifecycle (`submitted → working → artifacts → completed`).
- **go-upper** — calls go-echo *through the gateway* and answers
  `upper: <UPPERCASED>`; the echo marker check makes accidental pass-through
  impossible, proving agent-to-agent routing across Kyurem.

## Configuration

Single YAML file, validated at startup; scalar leaves are overridable via
`KYUREM_*` environment variables (see `config/kyurem.example.yaml`).
Validate without starting:

```bash
go run ./gateway/cmd/kyurem validate --config config/kyurem.example.yaml
```

## Development

```bash
make test   # gateway unit + integration tests (race detector)
make vet
```

The integration suite (`gateway/test/integration`) spins up real SDK demo
agents behind the real proxy handler and drives them with the official
`a2a-go` SDK client — discovery, round trips, SSE, card rewriting, trace
rules, unreachable-upstream 502 and the upper→echo chain.

Architecture decisions (module layout, routing/card rewriting, trace
propagation) are recorded under `docs/adr/`. The product requirements live
in `Kyurem Product Requirements Document.md`.
