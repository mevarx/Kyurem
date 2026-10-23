# Kyurem milestones

The PRD (§10) defines the milestone order. This file records what each
milestone delivers, how it is verified, and what is explicitly *not* in
scope. Milestones are done in order; a later milestone never starts before
the earlier exit criteria pass.

## M1 — Transparent proxy (complete)

Routing, streaming, card rewriting and caching, trace-context derivation,
timeouts, graceful shutdown, Prometheus metrics.

Exit criteria met: two demo agents communicate through the gateway, the
M1 demo script asserts every guarantee.

## M2 — Tracing (complete)

One span per handoff, parent/child trace tree, the TR-3 attribute set,
bounded asynchronous OTLP export with retry and drop counters, pinned GenAI
conventions, and the OTel collector plus ClickHouse in the compose stack.

Exit criteria met: one task renders as one trace tree (in-process
integration tests; the compose stack ships as `deploy/` and is validated
statically in CI).

## M3 — Dashboard MVP (complete)

**Scope.** The management API, the Overview / Task list / Task detail /
Agent graph / Costs pages, and cost accounting (CO-1 to CO-3).

**Design.**

| Piece | Where | Note |
|---|---|---|
| API contract | `api/openapi.yaml` | the single source of truth; the dashboard types are generated from it |
| Management API | `gateway/internal/mgmtapi` | own listener, bearer auth, versioned under `/api/v1` |
| Span store | `gateway/internal/storage` | `memory` (in-process ring) and `clickhouse` drivers |
| Query layer | `gateway/internal/storage/query.go` | one aggregation implementation over any store |
| Cost model | `gateway/internal/cost` | price table, per-span cost, unknown-vs-zero rules |
| Dashboard | `dashboard/` | React + TypeScript + Vite (PRD §5.4) |
| Deployment | `deploy/` | dashboard image, nginx proxy, compose service |

**Exit criteria (PRD §10).** *A scripted failure is diagnosable in under
60 seconds.* Concretely: the demo seeds a failing task, the dashboard shows
it in the task list, the task detail shows the failing span and the
root-cause hint, and the operator can name the failing agent without
opening a trace backend.

**Verification.**

- Go: unit tests for the query layer, both store drivers, the cost model
  and the API handlers; integration tests that drive real A2A traffic
  through the gateway and read it back through the management API.
- Demo: `demo/scenarios/m3-dashboard.sh` (and its PowerShell twin) boots the
  gateway, provokes a failure and asserts that the management API names the
  failing span and offers a root-cause hint. CI runs it: the exit criterion
  is executed, not just described.
- Dashboard: `typecheck`, `lint`, component tests and a production build in
  CI (`.github/workflows/dashboard.yml`).
- Deploy: the compose file is validated in CI; the full stack (collector →
  ClickHouse → dashboard) needs Docker and is a manual step.

**Not in M3.** Card signatures and the policy engine (M5), the circuit
breaker and failover (M6), payload capture (TR-4), budget alerts (CO-4) and
in-UI policy editing (P2). The dashboard shows those states as "not
available yet" instead of pretending they exist.

## M4 — Hardening and benchmarks (complete)

Metrics polish (stream-cap rejection counter, one bounded card-serving
instrument), graceful-shutdown and connection-draining tests, tiered load
tests at 100/1k/5k/10k concurrent SSE streams, and a published benchmark
document with reproducible scripts (`docs/benchmarks.md`,
`demo/scenarios/bench-load.*`).

**Verification.** `go test -race ./...` in CI runs the 100-stream smoke
tier plus the unit and integration suites; the 1k/5k/10k tiers are opt-in
via `KYUREM_LOAD_FULL=1k,5k,10k`. The real-process table in
`docs/benchmarks.md` was produced by the benchmark script on Windows 11.

**Known gap.** Official `a2a-tck` / `a2a-itk` conformance runs are not
wired into CI. The in-process suites exercise the A2A wire shape (JSON-RPC,
streaming, cards) but are not a substitute for the official TCK. This gap
is documented rather than claimed as passing; adding a conformance workflow
is follow-up work.

## M5 — Trust and policy

Card signature verification (TP-1 to TP-5), the policy engine, the Agents
and Policy/audit pages, and enforce mode.

## M6 — Failure handling

Retries with a budget, per-upstream circuit breakers, failover routing, and
the retry/failover spans (FO-1 to FO-4).

## M7 — Polish

Documentation, the demo walkthrough, release images, and the remaining P2
items.
