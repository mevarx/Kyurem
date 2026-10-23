# Kyurem benchmarks

Methodology, reproducible scripts and measured results for the M4 load tiers.
Kyurem's hot path is a streaming A2A proxy: every number below is
end-to-end, from the load generator's POST to the last byte read off the
SSE stream, through the real gateway process and a real (or in-process)
echo agent.

## Reproducing

### In-process tiers (smoke tier in CI, `go test`)

```sh
cd gateway
go test ./test/bench -count=1 -v                       # 100-stream smoke tier
KYUREM_LOAD_FULL=1k,5k,10k go test ./test/bench -count=1 -v
```

- No build step, no ports, no Docker. The fake agent is an in-process
  `httptest.Server` speaking the A2A v1.0 wire exactly as `a2a-go` does.
- CI runs the default `go test -race ./...`, which includes the 100-stream
  smoke tier. The 1k/5k/10k tiers are opt-in via `KYUREM_LOAD_FULL` and are
  not part of the CI pipeline.
- The smoke tier runs under `-race`; the heavy tiers are functional only.
- Assertions: every request reaches a terminal state, every stream carries
  exactly 6 relayed SSE events (submitted, working, 3 artifacts, completed),
  the active-streams gauge drains to zero, and the proxied-request counter
  is within 0.5% of the attempt count.

### Real-process tiers (published numbers)

```sh
pwsh demo/scenarios/bench-load.ps1                      # 1k, 5k, 10k
$env:KYUREM_BENCH_TIERS = '100'; pwsh demo/scenarios/bench-load.ps1
$env:KYUREM_BENCH_MODE  = 'send'; pwsh demo/scenarios/bench-load.ps1
```

A POSIX twin (`demo/scenarios/bench-load.sh`) runs the same flow. The
script builds the three binaries, allocates free loopback ports, boots
`go-echo` + `kyurem`, warms the card cache, runs each tier, scrapes
`/metrics` before/after and writes per-tier JSON to
`demo/bin/bench-results/`. Those JSON files are the raw data behind the
tables below; paste the table rows from the script's stdout into this
file when re-running.

## Tiers

Each tier opens N concurrent A2A exchanges and keeps them in flight for the
duration (closed loop, one request per worker). The echo agent emits 6 SSE
events per stream with a 40 ms inter-chunk sleep, so per-stream latency is
dominated by the agent's own pacing, not the gateway.

| tier | streams | duration | ramp | mode |
|---|---|---|---|---|
| 100  | 100   | 5 s  | 200 ms  | stream |
| 1k   | 1,000 | 20 s | 2 s     | stream |
| 5k   | 5,000 | 30 s | 5 s     | stream |
| 10k  | 10,000| 30 s | 10 s    | stream |

## Results — in-process smoke tier (verified, `go test`)

Measured on Windows 11, Go 1.26.5, `go test ./test/bench -count=1`:

| tier | streams | completed | failed | p50 ms | p95 ms | p99 ms | mean ms | rps | events/stream |
|---|---|---|---|---|---|---|---|---|---|
| 100-stream | 100 | 100 | 0 | 12.0 | 20.0 | 23.0 | 12.1 | 1006.5 | 6.0 |

Gateway: proxied requests +100, active-streams gauge returned to 0, no
goroutine leak beyond the 256-goroutine slop.

## Results — real-process tiers (Windows, retries=20, ramp 3 s/1k)

Measured on Windows 11, Go 1.26.5, machine 10.7.132.198, with
`retries=20` and `ramp_ms_per_1k=3000` to absorb Windows accept-queue
pressure. These are `--requests N` runs (one request per worker), so
`wall_seconds` is the time for all N exchanges to complete, not a
steady-state duration run.

| tier | streams | completed | failed | p50 ms | p95 ms | p99 ms | mean ms | rps | events/stream | peak streams |
|---|---|---|---|---|---|---|---|---|---|---|
| 1k-stream  | 1,000  | 1,000 | 0 | 119  | 195  | 237  | 125.3 | 333.1 | 6.0 | 104 |
| 5k-stream  | 5,000  | 5,000 | 0 | 320  | 824  | 1152 | 368.0 | 332.6 | 6.0 | 565 |
| 10k-stream | 10,000 | 10,000| 0 | 597  | 1969 | 3055 | 793.2 | 332.6 | 6.0 | 392 |

Notes on the 10k tier: 39 requests returned HTTP 502 and were retried
successfully (errors map `http_status_502=39`); `retried=39`. The
gateway's proxied-request counter charged 15,947 of 10,000 attempts
because the 5k tier's traffic was still being accounted on the same
process (cumulative counter, not reset between tiers) — see "Threats to
validity". Throughput is flat at ~333 rps across tiers because the
bottleneck is the echo agent's 40 ms inter-chunk sleep, not the gateway.

## Threats to validity

- **Windows accept queue.** `httptest`/`net.Listen` default backlogs on
  Windows shed bursts; the ramp and `retries=20` exist to absorb this.
  The published real-process table was taken on Windows 11, where the
  listener backlog, not the gateway, is the load-bearing constraint. The
  Linux CI smoke tier is not a comparable throughput measurement.
- **Cumulative counters.** `kyurem_proxy_requests_total` and the byte
  counters are process-lifetime; tiers run back-to-back on one gateway
  process, so deltas must be taken between scrapes (the driver does
  this for the table above; the JSON keeps both snapshots).
- **Goroutine pooling.** The driver's pooled idle connections linger past
  a run, so the goroutine delta is informational only; the smoke tier
  gates on a 256-goroutine slop.
- **No latency/throughput gates.** The load tests assert correctness
  (completion, relay integrity, gauge drain, counter tolerance), not
  performance. Published numbers are observations, not SLAs.
- **Conformance.** Official `a2a-tck` / `a2a-itk` runs are not wired into
  CI; the in-process bench stack covers the wire shape but not the full
  TCK suite. This is a documented M4 gap (see
  [`milestones.md`](milestones.md#m4--hardening-and-benchmarks-complete)),
  not a claim made by the published numbers.
