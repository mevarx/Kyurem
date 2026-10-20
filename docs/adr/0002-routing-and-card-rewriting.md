# ADR-0002: Routing model and agent-card rewriting on the data path

- Status: accepted
- Date: 2026-10-20
- Deciders: Kyurem maintainers
- Tags: gateway, routing, discovery

## Context

GW-1..GW-4 require a *transparent* A2A proxy: agents are exposed under the
gateway at `{route_prefix}/{agent}` (`/a2a/echo` by default), discovery
keeps working for standard A2A clients, and the upstream agent and its
clients must not need modification. The A2A v1.0 spec places identity and
protocol negotiation in the **agent card** (`GET /.well-known/
agent-card.json`): a card advertises `supportedInterfaces` — URL, protocol
binding (JSONRPC / HTTP+JSON / GRPC), and protocol version — and clients
combine card discovery with interface URLs to choose an endpoint. The
gateway therefore has two equally important jobs on the data path:

1. forward traffic so that A2A method dispatch keeps working end to end;
2. serve *rewritten cards* so that discovery loops back through the
   gateway rather than bypassing it.

## Decision

1. **Route table over prefix rewriting.** The registry holds one `Entry`
   per configured agent, keyed by case-insensitive name plus optional
   aliases; each entry may override its listen path. Inbound paths resolve
   to `(entry, configured path, suffix)` by longest-prefix match on the
   *escaped* path, matching case-insensitively for operator convenience.
   The suffix (services the A2A surface: `""` for JSON-RPC at the root,
   `/.well-known/agent-card.json` for discovery, HTTP+JSON suffixes such as
   `/message:send` or `/tasks/{id}:cancel`) is preserved **raw** and
   re-attached to the upstream URL, because REST-verb suffixes and
   percent-encoded task IDs must survive end to end.

2. **JSON-RPC traffic POSTs to the interface-URL root; REST traffic
   carries suffixes.** That shapes how the gateway infers an upstream for
   a request: sourced from the entry's upstream list — with annotation
   hints (`binding:`) winning when they match the request's inferred
   binding, otherwise the first upstream.

3. **Cards are fetched per agent, cached, and rewritten.** Upstream cards
   are fetched from`{upstream origin}/.well-known/agent-card.json`
   (optionally `card_url`), cached with TTL + ETag revalidation and **short
   stale-grace serving** (GW-4). When the gateway serves a card, it
   rewrites every interface URL back to the path the *request arrived on*
   (`{public_base_url or request scheme+host}{route_prefix}/{agent}`) with
   the binding preserved; **gRPC interfaces are dropped from served cards**
   (GW-8 passthrough is a separate later milestone, so advertising a
   binding the gateway cannot yet speak would break clients). ETag/304
   revalidation semantics apply after rewriting.

4. **Cards are untrusted input.** A hard `max_card_bytes` bound and strict
   JSON decoding apply before a card enters the cache or a served rewrite.

## Why this shape

- Transparent in both directions: no A2A client or agent code changes; a
  normal SDK client resolves a card through the gateway and all calls
  afterwards hit gateway routes by construction.
- The rewrite target depends on *which path served the card* — an alias
  path (e.g. `/a2a/greeter`) yields the alias URL, keeping every discovery
  route self-consistent.
- Keeping card rewriting in the gateway (not "more deliberately in the
  client") maintains the single entry point property the PRD's security
  and observability goals rely on.

## Consequences

- gRPC-bearing cards served through the gateway lose their gRPC interface
  until GW-8 exists; pure JSON-RPC/REST demos remain fully functional.
- `_File server ` passes routing semantics unchanged behind `/a2a/*`;
  the admin surface is a separate listener and cannot be reached through
  the data path.
- Case-insensitive routing trades a little spec-pristineness (paths are
  case-sensitive per RFC 3986) for operator ergonomics; the configured
  casing always wins for URL generation.

## Alternatives considered

- **Header-host proxying only** (no rewrite): keeps upstream cards
  verbatim but breaks standard discovery because clients then contact the
  upstream directly, bypassing all gateway benefits.
- **Per-route virtual hosts**: heavier operator setup; no protocol need.
- **Rewrite cards upstream-side** via injected configuration: incompatible
  with the "transparent proxy" contract (agents would need to know about
  the gateway).
