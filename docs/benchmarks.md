# Benchmarks

## Current real-process run

The following run uses the checked-in `demo/scenarios/bench-load.sh` harness in stream mode. The gateway, Go echo agent, and load generator shared one Linux x86_64 Modal sandbox configured for **2 vCPU / 8 GiB RAM**. Go was **1.26.0**; the virtual CPU model was not exposed. Concurrent load ran for the harness's configured tier durations with the default stream pacing, ramp, and retry policy. No external machine was involved.

RSS is the gateway process's resident set size before and after each tier, as sampled from the admin metrics. The delta is per-tier growth, not total host memory; the benchmark process and demo agent also consume host memory.

| Configured streams | Completed / failed | p50 / p95 / p99 latency (ms) | Mean latency (ms) | Peak active streams | Gateway RSS before → after (Δ MiB) | Throughput (req/s) |
|---:|---:|---:|---:|---:|---:|---:|
| 1,000 | 5,015 / 0 | 3,109 / 7,159 / 9,039 | 3,305 | 235 | 54.0 → 231.2 (+177.2) | 245.0 |
| 5,000 | 5,697 / 0 | 13,289 / 24,702 / 26,048 | 12,976 | 282 | 231.2 → 1,022.9 (+791.7) | 184.6 |
| 10,000 | 847 / 10,555 | 19,997 / 28,822 / 29,287 | 21,266 | 22 | 1,022.9 → 1,342.4 (+319.6) | 25.6 |

The 10,000-stream tier failed to sustain its configured target on this 2-vCPU host. Its latency percentiles describe the requests that completed during the overloaded interval; they are not a successful 10k-capacity result. The raw run recorded 9,250 request sessions failing to reach terminal state by the end, with transient HTTP errors and retries. The high RSS baseline at 10k reflects retained process memory from earlier tiers in the same benchmark process.

Reproduce with:

```sh
KYUREM_BENCH_TIERS=1k,5k,10k \
KYUREM_BENCH_OUT=demo/bin/bench-results \
bash demo/scenarios/bench-load.sh
```

Raw JSON and process logs are written to `demo/bin/bench-results/`; this directory is ignored by Git. Results are observational and not performance gates or an SLA.

## In-process smoke

CI also runs the 100-stream in-process SSE smoke with the race-enabled Go test suite. The recorded smoke sample completed 100 requests with no failures, p50/p95/p99 of 12/20/23 ms, mean 12.1 ms, and 1,006.5 requests/s. It exercises streamed event delivery, active-stream gauge drain, request-counter tolerance, and goroutine cleanup.

## Historical Windows run

An earlier real-process run documented on Windows 11 with Go 1.26.5 reported p50/p95/p99 of 119/195/237 ms at 1k, 320/824/1,152 ms at 5k, and 597/1,969/3,055 ms at 10k. The original run did not record CPU model or RAM, so it is kept as historical context only and is not combined with the current memory measurements. Its results were limited by the echo agent's 40 ms inter-chunk delay and Windows accept-queue pressure.
