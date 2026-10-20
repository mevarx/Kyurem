# ADR-0003: Trace-context derivation at the gateway edge

- Status: accepted
- Date: 2026-10-20
- Deciders: Kyurem maintainers
- Tags: gateway, observability, tracing

## Context

GW-5 asks the gateway to participate in distributed tracing between A2A
clients and agents. Two competing forces:

* Clients and agents emit W3C `traceparent`/`tracestate` headers, but not
  all of them will (upgrades without OTel exporters land in M2).
* The PRD demands that telemetry can **never** break traffic (§2.3/§9);
  observability must be best-effort by construction.
* A span should exist per gateway hop: "one extra hop inserted on the wire"
  must be visible in traces, both for debugging routing decisions and for
  the M2+ agent-fanout topology.

## Decision

1. **Derive on inbound; never block.** Each data-path request gets a
   trace decision made early in forwarding:

   - Valid inbound `traceparent` → same **trace-id**, **new span-id**
     (the gateway hop is a new span), flags copied verbatim.
   - Absent or malformed `traceparent` → the gateway **mints** a fresh
     gateway root (`00-<new 32-hex id>-<new 16-hex id>-01`) so agents
     behind the gateway still get a usable, correlated context; it is not
     a hard error to arrive without one. (`tracestate` is relayed
     verbatim in both cases and dropped when absent.)
2. **Never crash the request for telemetry reasons.** All telemetry steps
   (trace derivation included) run strictly before the proxy step in a
   non-allocating-on-failure path; a failure to mint or parse yields an
   *omitted* header (never a 5xx). OTel export wiring (M2) keeps the same
   fail-open contract: exporter errors are counters in `kyurem_*` gauge
   space, never request failures.
3. **Header propagation is explicit and spanner-shaped.** The outbound
   set is exactly `traceparent` + `tracestate` (no `trace-id`; no baggage
   propagation until M2). The inbound headers arrive already cloned by the
   forward path, but we *overwrite* the outbound `traceparent` rather than
   relay, so the gateway hop is materialized as its own span.

## Rationale

- Spec compliance: relaying semantics follow the W3C Trace Context Level 1
  standard — gateways proxying across trust boundaries are trace propagators
  with the right to restart spans downstream.
- Fail-open: a telemetry bug cannot 5xx a data path (PRD's guarantee).
- Cheap: derivation is pure string/format work against already-present
  bytes; minting uses a random `crypto/rand` value only when needed.

## Consequences

- Agents behind the gateway always observe a well-formed `traceparent` as
  long as the gateway is healthy; true end-to-end traces for clients
  *without* headers root at the gateway (intended — the exercise is "trace
  identification of gateway hops").
- Migrating to full OTel export in M2 keeps this same derivation rule; only
  the export layer changes.
- `tracestate` is relayed untouched: vendor ownership stays with whoever
  set it; the gateway adds no vendor entry until it has a real span store.

## Alternatives considered

- **Pass-through only** (always relay inbound, never mint): simplest, but
  agents observe missing headers for non-instrumented clients and partial
  flows remain uncorrelated — poor DX for the demo and M2-fail scenarios.
- **Reject malformed traceparents** (4xx): spec-purist but fragile clients
  crash data flows for observability reasons, violating the PRD guarantee.

## M2 implementation note (2026-10-20)

M2 replaced the hand-rolled derivation with the OpenTelemetry
`TraceContext` propagator (`gateway/internal/telemetry/telemetry.go`); the
manual helpers in `gateway/internal/proxy/tracectx.go` were deleted. The
observable rule did not change:

- valid inbound `traceparent` → same trace ID, **new** gateway span ID,
  inbound flags and `tracestate` preserved;
- absent or malformed `traceparent` → a fresh gateway root (head-sampled by
  `telemetry.sample_ratio`);
- the outbound request always carries the gateway's own `traceparent`, so
  the hop is materialized as a span (never a relay);
- propagation is fail-open: a propagation problem never changes an HTTP
  status.

What M2 adds around this rule: the gateway now *emits* the hop as a SERVER
span named `invoke_agent {callee}` and the parent of that span is the
caller's context, so a chain of agents is one trace tree (TR-1/TR-2). The
M1 tests that asserted the derivation rules were kept and now assert them
through the OTel propagator. See ADR 0004 for the export pipeline and the
caller-attribution rules, and `docs/telemetry-conventions.md` for the
attribute contract.
