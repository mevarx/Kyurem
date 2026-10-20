# ADR-0005: Management API, storage drivers and one aggregation layer

- Status: accepted
- Date: 2026-10-20
- Deciders: Kyurem maintainers
- Tags: gateway, management-api, dashboard, m3

## Context

M3 adds a read-only management API and a dashboard (PRD §4.6, §5). The API
must answer questions about tasks, agents, call graphs and costs that are
derived from the spans M2 produces. Three constraints shaped the design:

* The A2A data path must stay transparent. The API is an *observer*, never
  a participant in a call.
* The dashboard must work in two very different deployments: the compose
  stack (ClickHouse behind the collector) and a laptop (no database at
  all).
* The PRD demands that the query API never exposes raw SQL to the
  dashboard (§6.2) and that unknown data is shown as unknown, never as
  zero (§5.1).

## Decision

1. **A separate, authenticated, versioned listener.** The API lives on
   `listeners.mgmt.addr` (default `127.0.0.1:8082`), separate from the A2A
   data path and from the admin listener. It answers under `/api/v1` and
   requires `Authorization: Bearer <token>`. A token is mandatory when the
   listener is not loopback: config validation rejects an open management
   API on a public address.
2. **Two store drivers behind one interface.** `storage.Store` returns raw
   spans for a window; `memory` keeps a bounded in-process ring (no
   dependencies, used by local development and the tests) and `clickhouse`
   reads the table the collector writes. The ClickHouse driver speaks the
   HTTP interface with the standard library only — no driver dependency —
   which also makes it testable without a database.
3. **One aggregation implementation.** `storage.Queries` implements the
   task grouping, span trees, root-cause heuristic, graph, cost roll-ups
   and overview *once*, on top of any store. Both drivers therefore answer
   identically, and the dashboard sees one set of semantics.
4. **A task is a trace.** One A2A task is one trace tree, so the task list
   groups spans by trace; the task id is the one the root hop reported.
   Grouping by task id instead would split a chain whose nested hop starts
   its own task.
5. **The root cause is a labelled heuristic.** The API returns the
   earliest failing span and the spans it affected, with
   `confidence: heuristic`. The dashboard must present it as a hint.
6. **Unknown is null.** Cost and token fields are pointers: a missing
   value is `null`, never `0`. Filters that need a cost exclude tasks
   whose cost is unknown instead of treating them as free.

## Rationale

- One listener per concern keeps the data path free of policy: an
  overloaded or unauthenticated dashboard cannot slow or break A2A calls.
- The driver split removes the "works only with Docker" trap: the laptop
  story is a first-class deployment, not a degraded one.
- Implementing the aggregation once removes the classic driver drift, where
  the local development view and the production view disagree.
- The ClickHouse HTTP interface avoids a heavy driver dependency for one
  query shape, and keeps the query layer free of SQL strings.

## Consequences

- The ClickHouse driver pulls the spans of a window into memory before
  aggregating. That is fine at demo scale, but a long window over a large
  table needs pre-aggregated tables (materialized views) — planned with
  the M3 dashboard work and the M4 benchmarks.
- The in-process ring is bounded (default 4096 spans): it is a recent
  activity view, not a history.
- The bearer token is shared state: the dashboard needs it (a build-time
  variable or a field in Settings). M5 adds proper identities; until then
  the token is a single shared secret, and it is never logged.
- `management.auth_token` must be set for any non-loopback deployment; the
  compose stack sets a demo token through the environment.

## Alternatives considered

- **Serve the API from the admin listener.** Fewer listeners, but the
  admin listener is the operational surface (probes, metrics); mixing a
  token-protected API into it makes both harder to reason about.
- **Let the dashboard query ClickHouse directly.** No API, but it leaks the
  schema into the frontend, breaks the "the API never exposes raw SQL"
  rule, and cannot serve the in-memory case at all.
- **One driver (ClickHouse only).** Simpler, but the dashboard would be
  unusable without Docker, and the demo story would suffer.
- **Compute the aggregations in SQL per driver.** Faster at scale, but the
  semantics would exist twice (SQL and Go) and drift — the opposite of
  what the PRD asks for.
- **Report a zero cost when usage is unknown.** Cheaper to render, but it
  is a lie: the dashboard would show free traffic that was never priced.
