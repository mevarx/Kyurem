# Kyurem milestones

Milestones M1–M7 are implemented in the repository. The checks listed here describe the maintained verification paths; they do not imply that an external A2A conformance suite or a remote GitHub Actions run has been performed for every change.

| Milestone | Status | Delivered scope |
|---|---|---|
| M1 — Transparent proxy | Complete | A2A routing, aliases, streaming, card handling, timeouts, and graceful shutdown |
| M2 — Tracing | Complete | Handoff spans, task trace trees, bounded asynchronous OTLP export |
| M3 — Dashboard MVP | Complete | Management API, span queries, dashboard diagnostics, and token/cost accounting |
| M4 — Hardening and benchmarks | Complete | Metrics, limits, shutdown tests, reproducible benchmarks, SSE load tiers |
| M5 — Trust and policy | Complete | Caller identity, signed-card verification, authorization, audit, dashboard views |
| M6 — Failure handling | Complete | Bounded retries, per-upstream breakers, ordered failover, trace events |
| M7 — Polish and handoff | Complete | Updated README, milestone/config docs, current examples, deployment definitions |

## M1 — Transparent proxy

Routes A2A traffic by agent name or alias, preserves streaming (including SSE), rewrites and caches agent cards, propagates W3C trace context, applies configured upstream timeouts, and drains connections on shutdown. In-process integration tests exercise the proxy, and the M1 demo verifies calls between demo agents.

## M2 — Tracing

Creates an OpenTelemetry span per handoff and links task hops into trace trees. OTLP export is asynchronous and bounded so collector outages drop telemetry rather than block proxy traffic. The Compose deployment includes the collector and ClickHouse stack; telemetry conventions and configuration are documented under `docs/` and `deploy/`.

## M3 — Dashboard MVP

Provides a separate authenticated management listener, versioned read API, span storage/query layer, and dashboard views for overview, tasks, task details, agent graphs, agents, costs, and settings. Cost accounting uses reported token usage; absent usage stays unknown. The scripted failure walkthrough exercises the diagnosis path.

## M4 — Hardening and benchmarks

Adds request and SSE limits, shutdown/draining checks, metrics, race-enabled Go checks, benchmark scripts, and SSE load tiers at 100, 1,000, 5,000, and 10,000 streams. CI runs the 100-stream smoke tier; larger runs remain opt-in and have methodology in `docs/benchmarks.md`.

**Known gap:** the official `a2a-tck` and `a2a-itk` conformance suites are not wired into GitHub Actions. Repository integration tests cover selected protocol behavior but do not claim official conformance.

## M5 — Trust and policy

Supports bearer-token and optional mTLS caller identity, agent-card signature verification against operator-configured public keys, per-agent trust modes, deny-overrides authorization, a bounded audit ring, decision endpoints, telemetry events, and dashboard policy/audit views. The gateway never fetches a signer-provided key URL. Tests cover credential rejection/acceptance, signed and unsigned cards, policy enforcement, and configuration safety.

**Skill-policy boundary:** the A2A request does not contain an authoritative skill selection. `X-Kyurem-Skill` is a caller-provided hint, so skill-scoped rules are available in `warn` mode for audit only; config validation rejects them in `enforce` mode rather than treating a spoofable header as an authorization fact.

## M6 — Failure handling

Uses the configured upstream order within the selected transport binding. Each upstream has independent consecutive-failure circuit state with a single half-open probe. A per-agent fixed-window retry budget and a maximum-attempt setting bound amplification. Network failures and transient 502/503/504 responses can advance to another upstream or retry on the next eligible attempt; other 5xx responses affect breaker state but are returned directly.

Retries are conservative: GET/HEAD/OPTIONS without a body can be retried; a POST requires an `Idempotency-Key` and a body within `max_retry_body_bytes`. The upstream must implement deduplication for that key. Unkeyed or oversized/streaming request bodies are not replayed. Retries and failovers are recorded as events on the existing handoff span. Unit and end-to-end gateway tests cover failover, keyed-body replay, retry budgets, open circuits, and half-open recovery.

## M7 — Polish and handoff

Rewrites the README as the full M1–M7 guide, refreshes milestone and configuration docs, keeps local and Compose examples current, and ignores generated test/coverage outputs. Gateway, dashboard, demo, and Compose Dockerfiles remain in the repository for reproducible builds.

**Not included:** automatic publication of release images is not configured. Compose files are syntax-validated by CI; running the full local stack is a Docker-dependent manual check.

## Verification entry points

- `make test`, `make test-demo`, and `make vet` cover the Go gateway and demo modules.
- `make validate` loads the shipped configs; the CI workflows also build, format-check, run race tests, and validate Compose.
- `make dashboard-check` and `make dashboard-build` run the dashboard checks and production build.
- The M3 walkthrough is run in CI. The tracing/ClickHouse Compose stack and higher benchmark tiers are opt-in local workflows.
