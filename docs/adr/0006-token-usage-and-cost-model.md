# ADR-0006: Token usage, price table and the cost of a span

- Status: accepted
- Date: 2026-10-20
- Deciders: Kyurem maintainers
- Tags: gateway, cost, tracing, m3

## Context

PRD §4.3 asks the gateway to read token usage when the upstream reports it
(CO-1), to price it from a config table (CO-2) and to aggregate it per
agent, task and window (CO-3). The PRD is explicit about the hard part:
"the gateway cannot know token usage that agents do not report. The
dashboard must show 'unknown' rather than zero."

A2A has no dedicated usage field in the version the SDK implements, so the
gateway has to recognize what agents actually send. On top of that, the
gateway must not invent a model name: pricing a request against the wrong
model is worse than not pricing it.

## Decision

1. **Usage is read from a bounded payload prefix, tolerantly.** The
   gateway looks for a `usage` object anywhere in the response (or the
   request, as a fallback) and accepts the token spellings agents use:
   `input_tokens`/`inputTokens`/`prompt_tokens`/`promptTokens` and
   `output_tokens`/`outputTokens`/`completion_tokens`/`completionTokens`.
   A `model` string is picked up the same way. SSE frames are parsed line
   by line; a truncated prefix falls back to a regex. Only the bounded
   prefix (64 KiB) is inspected — the hot path never buffers a whole body.
2. **Reported usage is a span attribute, not a guess.** When the agent
   reported usage, the span carries `gen_ai.usage.input_tokens`,
   `gen_ai.usage.output_tokens`, `gen_ai.request.model` and
   `kyurem.tokens.known=true`. Without a report the span keeps
   `kyurem.tokens.known=false` and carries no usage attributes.
3. **The cost is computed when the span ends.** The gateway holds the
   price table (`pricing:` in the config) and writes `kyurem.cost.usd` on
   the span when it knows both the usage and the model. The model key is
   matched exactly first, then case-insensitively. A model without a price
   entry leaves the cost unknown: no attribute is written, never zero.
4. **Unknown propagates through the whole chain.** The query layer sums
   the known costs and reports `null` when no span had one; a bucket
   counts the spans whose usage was unknown so the dashboard can say
   "12 of 30 spans are unpriced" instead of pretending the rest was free.
5. **The demo agent reports usage so the path is exercisable.** The demo
   echo agent attaches `metadata.model` and `metadata.usage` to its reply
   (and to the task in the streaming path). The counts are word counts:
   deterministic and honest about being a demo. Real agents that report
   nothing keep their cost unknown.

## Rationale

- The span is the only durable record of a call. Computing the cost at
  span time means the dashboard aggregates a fact instead of re-deriving
  it, and a price-table change does not silently rewrite history (new
  spans get the new price; old spans keep theirs).
- Writing no attribute is the cleanest way to express "unknown": a
  missing `kyurem.cost.usd` cannot be confused with a free call.
- Tolerating several spellings is the price of a spec that has not
  settled; the alternatives (one exact shape) would silently report
  "unknown" for most agents.

## Consequences

- A price table change only affects new spans. The dashboard shows the
  price that was in force when the call happened, which is the honest
  reading of a historical cost.
- Usage extraction inspects up to 64 KiB of every response. That is the
  same bound the task-id extraction already uses, and the bytes are
  discarded when the span ends.
- The gateway cannot verify that reported usage is truthful; it records
  what the agent said. The demo agent's numbers are explicitly synthetic.
- Aggregations must distinguish "no spans" from "spans without a price".
  Both are represented: an empty bucket list and a non-zero
  `unknown_spans` count.

## Alternatives considered

- **Compute the cost in the dashboard from the price table.** Always
  consistent with the current table, but the dashboard would need the
  price table, the history would be rewritten on every price change, and
  the span would carry no cost of its own (PRD §6.1 expects
  `kyurem.cost.usd`).
- **Estimate the usage from the payload size.** Always available, but a
  guess presented as a number is exactly what the PRD forbids; the honest
  answer for an agent that reports nothing is "unknown".
- **Require one exact usage shape.** Cleaner code, but agents that use
  another spelling would silently look free.
- **Let the agent set the price.** No price table to maintain, but the
  cost would be whatever the caller claims.
