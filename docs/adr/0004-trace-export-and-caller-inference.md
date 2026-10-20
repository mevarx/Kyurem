# ADR-0004: Bounded trace export and caller inference

- Status: accepted
- Date: 2026-10-20
- Deciders: Kyurem maintainers
- Tags: gateway, observability, tracing, m2

## Context

M2 makes the gateway emit one span per handoff (TR-1) and requires a bounded,
retrying OTLP exporter with visible drop counters (TR-5). The OpenTelemetry
SDK already ships `BatchSpanProcessor`, but its queue overflow and dropped
spans are not surfaced as application metrics, and its retry accounting is
opaque. The PRD also demands that telemetry never blocks or fails A2A
traffic (§2.3/§9), including under a collector outage.

TR-3 asks for `kyurem.caller.agent`, but the gateway does not control agent
code. Some agents propagate W3C context (the demo `go-upper` does), while
others only add a header or nothing at all. A caller label that is wrong is
worse than one that says `unknown`; attribution therefore needs an explicit
precedence rule.

Finally, chained agents that forward a task must not leak the caller's
`TaskID` to an agent that has never seen that task. The A2A context, by
contrast, is the conversation identity and should survive the chain.

## Decision

1. **Use a custom bounded asynchronous span processor.** `OnEnd` performs a
   non-blocking send into a fixed-size channel. A background goroutine
   batches, exports and retries with exponential backoff. Every discarded
   span is counted with a reason (`queue_full`, `export_failed`, `shutdown`)
   before it is dropped. `ForceFlush` drains queued spans; `Shutdown` stops
   admission and drains within the caller's deadline. The processor is
   installed only when an exporter exists, but a real `TracerProvider` is
   always created so propagation and span behavior do not change with export
   being off.
2. **Resolve caller attribution with a bounded process-local tracker.**
   At span start the gateway remembers `(trace ID, span ID) -> callee, hop`
   in a 4096-entry ring. A nested handoff whose remote parent matches that
   entry is attributed to the callee and gets `hop + 1`. Otherwise the
   optional `X-Kyurem-Caller` hint is sanitized and used, and otherwise the
   caller is `unknown`. Lookups and eviction are non-blocking and O(1); the
   map cannot grow with traffic.
3. **Preserve `ContextID`, do not forward `TaskID`.** The chained demo agent
   sends its caller's context ID on the nested A2A message, so one
   conversation is one trace/task family. It creates a new task ID for its
   own upstream call. Forwarding the caller's task ID would address a task
   that the downstream agent does not own.
4. **Keep payload capture off and reject it in configuration.** TR-4 is not
   in M2. The gateway keeps at most 64 KiB of request/response prefix in
   memory solely for ID/status extraction and discards it at span end.
   `telemetry.payload_capture: true` is a startup validation error rather
   than a silently ignored option.
5. **Use the OTel W3C propagator instead of the M1 manual derivation.** The
   observable rule is unchanged (ADR 0003): same trace ID, fresh gateway
   span ID, preserved `tracestate`, fresh root when absent/malformed. The
   SDK implementation removes a parallel parser and gives the exporter the
   same parent the wire sees.

## Rationale

- **Fail-open by construction.** The hot path has one non-blocking channel
  send; exporter latency, retries and collector outages run in a goroutine
  that the request never joins.
- **Accounting is part of the contract.** A custom processor is the smallest
  way to expose per-reason drop counters without a second reader pipeline.
- **Caller attribution without agent changes.** The tracker turns the trace
  tree itself into the attribution source; the hint remains a fallback for
  non-propagating agents and is explicitly untrusted.
- **Bounded memory.** Queue, tracker and payload prefixes all have fixed
  caps, independent of request size or concurrency.
- **Task identity correctness.** Context is conversation-scoped; task IDs
  are agent-local. Forwarding a foreign task ID produces invalid chains.

## Consequences

- The gateway owns ~250 lines of exporter plumbing and must keep its
  shutdown/force-flush behavior aligned with the SDK provider contract.
- Caller inference is process-local. Behind multiple replicas, a nested
  call that crosses replicas falls back to the hint or `unknown`; making it
  global would require a shared store or a signed wire attribute and is
  deferred.
- `X-Kyurem-Caller` is attribution only. It must never be used for
  authorization; trust decisions arrive with the M5 policy engine.
- A collector outage costs spans, not traffic. Operators alert on
  `kyurem_telemetry_spans_dropped_total` and `kyurem_telemetry_queue_depth`
  instead of request errors.
- Payload capture remains a visible future feature rather than a latent
  privacy risk: the option fails startup until TR-4 is implemented.

## Alternatives considered

- **SDK `BatchSpanProcessor` plus a metrics bridge.** Less code, but queue
  overflow and per-reason drops are not observable, and retry behavior
  cannot be counted without wrapping internals.
- **Collector-side drop metrics only.** The collector cannot see spans the
  gateway already dropped at its queue boundary, which is exactly the
  overload case TR-5 calls out.
- **Header-only caller attribution.** Simple, but the hint is optional and
  spoofable; the trace tree gives a trustworthy answer whenever the caller
  propagates context.
- **Forward the caller's TaskID through chained agents.** Would make the
  downstream call refer to a task the downstream agent does not own; A2A
  task IDs are per-agent, while context IDs are the conversation key.
- **Buffer full payloads for extraction.** Violates the streaming/no
  whole-body buffering requirement; a 64 KiB prefix covers IDs and streamed
  status frames without changing memory behavior.
