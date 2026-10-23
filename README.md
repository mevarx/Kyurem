# Kyurem

Kyurem is a Go gateway for A2A v1.0 agents, with a React/TypeScript dashboard. It routes agent traffic without owning agent state, while tracing handoffs, accounting for reported token usage, checking configured trust and access rules, and handling upstream failures.

## Delivered scope

- **M1 — Transparent proxy:** routes by agent name and alias; streams requests and SSE responses; rewrites and caches agent cards; propagates W3C trace context; applies upstream timeouts and graceful shutdown.
- **M2 — Tracing:** creates an OpenTelemetry span per handoff, links task hops into trace trees, and exports asynchronously through bounded OTLP queues. Telemetry is dropped rather than allowed to block traffic during collector outages.
- **M3 — Dashboard MVP:** provides an authenticated management API on a separate listener and a read-only dashboard for overview, tasks, task details, agent graphs, agents, costs, and settings. Unknown token usage and cost remain unknown, not zero.
- **M4 — Hardening and benchmarks:** includes request and stream limits, shutdown/draining tests, race-enabled Go tests, reproducible benchmarks, and SSE load tiers up to 10,000 streams. CI runs the 100-stream smoke tier; larger tiers are opt-in.
- **M5 — Trust and policy:** supports bearer and optional mTLS caller identity, configured agent-card signature keys, deny-overrides policy, bounded audit records, decision APIs, and dashboard views. Card-signer-provided key URLs are never fetched. Skill filters are warn-only: A2A requests do not carry an authoritative skill selection, so enforce-mode skill rules are rejected at config validation.
- **M6 — Failure handling:** supports ordered same-binding failover, per-upstream circuit breakers, a bounded per-agent retry budget, and retry/failover events on the handoff trace. Retries are limited to safe methods or POST requests with an `Idempotency-Key`; the upstream must honor that key. Request bodies are replayed only when bounded by `max_retry_body_bytes`; streaming and other non-replayable calls are not retried.
- **M7 — Polish and handoff:** consolidates the milestone and configuration documentation, keeps example and Compose configs current, and provides deployment Dockerfiles and the local Compose stack. The repository does not publish release images automatically.

## Architecture and interfaces

- The A2A data listener defaults to `127.0.0.1:8080`; health, readiness, metrics, and version endpoints use `127.0.0.1:8081`.
- The management API uses its own listener (`127.0.0.1:8082` by default) and bearer authentication. The dashboard is in `dashboard/`.
- Configuration is strict YAML with startup validation and `KYUREM_*` scalar overrides. See [`config/kyurem.example.yaml`](config/kyurem.example.yaml), [`docs/configuration.md`](docs/configuration.md), and [`api/openapi.yaml`](api/openapi.yaml).
- Deployment assets include the gateway, dashboard, and demo Dockerfiles plus the Compose stack in `deploy/compose/`.

The sample configuration is intended for local development: caller authentication, card trust, and policy enforcement are off until explicitly configured. Do not expose a deployment using demo credentials or without setting the security modes and keys appropriate to that deployment.

## Quickstart

Requirements: Go 1.26+. Build and run the gateway and demo agents:

```sh
go build -o demo/bin/kyurem ./gateway/cmd/kyurem
go build -o demo/bin/go-echo ./demo/agents/go-echo
go build -o demo/bin/go-upper ./demo/agents/go-upper
go build -o demo/bin/demo-client ./demo/scenarios/client

./demo/bin/go-echo --port 9010 &
./demo/bin/kyurem serve --config config/kyurem.example.yaml &
./demo/bin/go-upper --port 9011 \
  --echo-card-url http://127.0.0.1:8080/a2a/echo/.well-known/agent-card.json &
```

Run the scripted proxy walkthrough with `make run-demo`. The tracing and dashboard walkthroughs are `make demo-m2` and `make demo-m3`; both require Docker for their local telemetry stack. The M6 retry, failover, and breaker behaviors are covered by backend tests.

## Failure-handling behavior

Retries default to at most two total attempts and ten additional attempts per agent per minute. The circuit breaker opens after five consecutive upstream network/5xx failures, stays open for 30 seconds, and then permits a single probe. Only transient HTTP 502, 503, and 504 responses are retried; other 5xx responses still count toward breaker state.

GET, HEAD, and OPTIONS requests without bodies are retryable. A POST is retryable only when it has an `Idempotency-Key` and its body can be replayed within the configured cap. The key is a contract with the upstream, not proof of idempotency; the upstream must deduplicate repeated requests. SSE response bodies are never replayed after headers have been sent. Configure ordered upstreams with the same binding to enable failover for that binding.

## Development and verification

```sh
make test              # gateway tests with the race detector
make test-demo         # demo-module tests
make vet
make validate          # shipped YAML configs
make dashboard-install # first-time dashboard dependencies
make dashboard-check   # typecheck, lint, and tests
make dashboard-build
```

GitHub Actions runs the gateway build/vet/format/race tests, config and Compose validation, the M3 scenario, demo-agent checks, and dashboard typecheck/lint/tests/build on pushes and pull requests. Starting the full Compose stack requires Docker. The official `a2a-tck` / `a2a-itk` conformance suites are not currently wired into CI; the in-repository integration tests are not a substitute for those suites.

More detail: [`docs/milestones.md`](docs/milestones.md), [`docs/benchmarks.md`](docs/benchmarks.md), and [`docs/telemetry-conventions.md`](docs/telemetry-conventions.md).
