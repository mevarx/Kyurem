# Benchmarks

## Why the original 10k tier failed

The old 10k tier (`1k,5k,10k`, short 3-word streams, 40 ms chunk delay) reported 847 completed / 10,555 failed. Investigation on the 2 vCPU / 8 GiB Modal sandbox (gVisor kernel `4.19.0-gvisor`; gateway, agent and load generator all on that one host):

| Question | Evidence | Conclusion |
|---|---|---|
| Was the gateway CPU-bound? | Gateway CPU profile (10 s mid-run): ~10% busy, 87% of samples in socket syscalls. Whole-host `ps` showed ~0.35 core average across all three processes. | No. The gateway was mostly idle. |
| Did the test measure concurrency? | A stream lasts ~240 ms, so "10,000 workers" is really ~40,000 new streams/s of connection churn. Gateway `active_streams` peaked at 22–340, never near 10,000. | The tier never held 10k streams; it stress-tested connection setup. |
| Is the harness/host alone enough to fail? | Load generator pointed **directly at the echo agent** (no gateway): 1k ok, 5k ok, 10k → 5,485 `connect_error`, 1,402 completed. | Yes. The 10k failure reproduces without the gateway. |
| What failed inside the gateway path? | Debug logs: `dial tcp 127.0.0.1:…: i/o timeout` (5 s connect timeout) and `timeout awaiting response headers` (30 s) towards the upstream, then the circuit breaker (5 consecutive failures, 30 s open) turned them into 503 `upstreams_unavailable`, which the load generator retried (3 attempts). 502/503 dominate the error map. | Upstream/host saturation, amplified by breaker + retries. |
| What is the demo agent doing? | Agent CPU profile: `encoding/gob` encode/decode (`compileDec`, `decodeStruct`) dominates; the a2a-go SDK's in-memory task store deep-copies the task per event. | `go-echo` is far heavier per stream than a real SSE relay needs to be. |

No separate load-generator machine was available (one sandbox only), so that experiment was **not** done. Instead the agent was isolated from the gateway by the synthetic upstream below, and the direct-to-agent run above shows the host cannot sustain the old churn pattern even without the gateway.

## Methodology (held concurrent streams)

`KYUREM_BENCH_HOLD=1` makes each worker open exactly one stream and keep it open; the tier fails unless the gateway's own `kyurem_proxy_active_streams` peaked at the full stream count (`--require-peak`) **and** returned to zero afterwards (`--drain-timeout`, default 30 s). Stream length is `words x chunk delay` (here 20 x 3 s = 57 s); the per-request timeout (120 s) exceeds it, the ramp is `streams / 330` seconds (<= ~330 new streams/s), and retries are left on and reported (`retried` in the JSON). Failures are never hidden: any failed request, undrained gateway or missed peak makes the script exit non-zero.

Two upstreams are supported:

- **SDK echo** (default): the real `go-echo` A2A agent via a2a-go.
- **Synthetic** (`KYUREM_BENCH_SYNTHETIC=1`, `go-echo --synthetic-stream`): same event shapes (submitted, working, one artifact per word, completed) from a minimal SSE writer with no per-event task-store work, so the tier measures the gateway.

```sh
KYUREM_BENCH_HOLD=1 KYUREM_BENCH_SYNTHETIC=1 \
KYUREM_BENCH_WORDS=20 KYUREM_BENCH_CHUNK_DELAY=3s \
KYUREM_BENCH_TIMEOUT=120s KYUREM_BENCH_DURATION=150s KYUREM_BENCH_SETTLE=15 \
KYUREM_BENCH_TIERS=1k,2k,5k,7.5k,10k \
KYUREM_BENCH_OUT=demo/bin/bench-results \
bash demo/scenarios/bench-load.sh
```

`KYUREM_PPROF=1` exposes `/debug/pprof` on the gateway admin port and `ECHO_PPROF=127.0.0.1:6060` on the agent; `KYUREM_BENCH_LOG_LEVEL=debug` logs per-failure causes. `KYUREM_BENCH_SETTLE=N` idles N seconds after each tier and prints gateway goroutines, RSS, heap and active streams. (`bench-load.ps1` does not have these options yet.)

## Results: held streams, synthetic upstream

Same sandbox, Go 1.26.0, revision with the fixes below, one gateway process across tiers (so RSS carries over). Latency is stream wall time, so ~57,000 ms is the floor; the excess is gateway + host overhead. "Settled" is 15 s after the tier with no load.

| Streams | Completed / failed | p50 / p95 / p99 ms | Peak active | Gateway CPU s | Settled goroutines / RSS MiB / active |
|---:|---:|---:|---:|---:|---:|
| 1,000 | 1,000 / 0 | 57,017 / 57,173 / 57,281 | 1,000 | 1.5 | 143 / 193 / 0 |
| 2,000 | 2,000 / 0 | 57,014 / 57,068 / 57,183 | 2,000 | 2.7 | 144 / 319 / 0 |
| 5,000 | 5,000 / 0 | 57,016 / 57,185 / 57,271 | 5,000 | 7.1 | 143 / 714 / 0 |
| 7,500 | 7,500 / 0 | 57,034 / 57,341 / 57,417 | 7,500 | 11.1 | 143 / 1,038 / 0 |
| 10,000 | 10,000 / 0 | 57,088 / 57,493 / 57,588 | 10,000 | 18.3 | 143 / 1,376 / 0 |

Drain: active streams were 0 immediately after every run (`drain_seconds` <= 0.6). Goroutines settle at 143 = baseline 15 + 128 for the bounded upstream idle-connection pool (64 idle conns x 2). Repeating a 2k tier three times in one process gave settled RSS 402 / 425 / 430 MiB, i.e. a plateau, not a leak. RSS scales with peak concurrency (~140 KiB per held stream at 10k) because Go keeps the heap; heap in use after settling is ~270 MiB.

**Repeatability (10k, synthetic upstream):**

| Ramp | Runs | Result |
|---|---|---|
| 30 s (<= 330 streams/s, the script default) | 9 (8 consecutive + the table above) | 9/9 pass: 10,000 completed, 0 failed, peak 10,000, drained. p50 57.1-57.5 s, p99 57.5-61.1 s, gateway CPU 15.8-18.3 s |
| 10 s (~1,000 streams/s) | ~15 | 2 failed (463 and 1,382 failures); the rest passed |

The two failing 10 s-ramp runs were `dial tcp 127.0.0.1:…: i/o timeout` (5 s connect timeout) and 30 s response-header timeouts to the upstream during the connection burst; the breaker then rejected retries with 503. This is a connection-establishment limit of the shared sandbox (listen backlog 1,024, gVisor loopback), not a stream-capacity limit: the same 10,000 held streams pass when opened at <= 330/s. The timeouts and breaker settings were deliberately **not** loosened. A 1,000 streams/s arrival burst on this host is therefore **not** reliable; 10k held streams at a realistic arrival rate is.

## Results: held streams, real SDK echo agent

30 words x 3 s (87 s streams), ramp 3-30 s depending on tier (old default ramps), timeout 200 s:

| Streams | Completed / failed | p50 ms | Notes |
|---:|---:|---:|---|
| 1,000 | 1,000 / 0 | 87,193 | ok |
| 5,000 | 5,000 / 0 | 104,941 | ok, but streams run ~20% longer than the 87 s floor |
| 10,000 | 3,590 / 6,410 | 101,303 | **fails**: 502 (2,433) and 503 (16,797) after the upstream stops answering |

With the SDK-based demo agent, 10k concurrent streams do **not** pass on this host. Its per-event gob deep copies (see above) saturate the shared CPUs first; the gateway is not the limiting component. This is a demo-agent/host limit and is reported as such.

## Fixes made (each backed by evidence)

1. **Per-stream copy buffer 32 KiB -> 8 KiB** (`proxy.copyBufferSize`). Heap profile at 10k held streams: `proxy.New.func1` (the buffer allocation) was the single largest in-use allocation, 125.9 MiB of 301 MiB, because `ReverseProxy` keeps one buffer per open response. Settled RSS after the 10k tier went from ~2.1 GiB (before) to ~1.4-1.7 GiB (after); tier order differed, so treat that as indicative. Test: `TestBufferPoolUsesCopyBufferSize`.
2. **Rate-limited the "telemetry sink queue full" warning** (`telemetry.sinkProcessor`). Under overload this logged once per dropped span; the goroutine dump showed 189 request goroutines blocked on the logger's mutex in `OnEnd`. It now logs at most once per second with the dropped count. Test: `TestSinkProcessorRateLimitsDropWarnings`.
3. **Harness**: gateway drain verification (`awaitDrain`, tests in `drain_test.go`), `--require-peak`, a scrape that retries instead of reporting zeros when `/metrics` is slow (previously "after" snapshots could be blank), closing the client's idle connections before measuring, configurable stream length/ramp/timeout, `KYUREM_BENCH_SYNTHETIC`, `go-echo --chunk-delay/--synthetic-stream/--pprof-addr` (test: `TestSyntheticStreamEmitsLifecycleAndFallsThrough`), and new 2k / 7.5k tiers. No gateway timeout, limit or breaker setting was changed.

## Not done / caveats

- Load generator, gateway and upstream still share one host; a separate load-generator machine was not available. gVisor networking makes absolute connection-rate numbers pessimistic versus bare metal.
- The circuit breaker counts overload-induced dial timeouts as upstream failures, so a connection burst can turn a few timeouts into ~30 s of 503s. This was observed, not changed.
- Results are observational, not performance gates or an SLA.

## Historical: original short-stream run (superseded)

Short 3-word streams, default pacing, `KYUREM_BENCH_TIERS=1k,5k,10k`:

| Configured streams | Completed / failed | p50 / p95 / p99 latency (ms) | Peak active streams | Throughput (req/s) |
|---:|---:|---:|---:|---:|
| 1,000 | 5,015 / 0 | 3,109 / 7,159 / 9,039 | 235 | 245.0 |
| 5,000 | 5,697 / 0 | 13,289 / 24,702 / 26,048 | 282 | 184.6 |
| 10,000 | 847 / 10,555 | 19,997 / 28,822 / 29,287 | 22 | 25.6 |

Reproduce with `KYUREM_BENCH_TIERS=1k,5k,10k bash demo/scenarios/bench-load.sh`. This mode measures connection churn, not held concurrency, and still fails at 10k here for the reasons above. Raw JSON and logs go to `demo/bin/bench-results/` (git-ignored).

## In-process smoke

CI also runs the 100-stream in-process SSE smoke with the race-enabled Go test suite. The recorded smoke sample completed 100 requests with no failures, p50/p95/p99 of 12/20/23 ms, mean 12.1 ms, and 1,006.5 requests/s. It exercises streamed event delivery, active-stream gauge drain, request-counter tolerance, and goroutine cleanup.

## Historical Windows run

An earlier real-process run documented on Windows 11 with Go 1.26.5 reported p50/p95/p99 of 119/195/237 ms at 1k, 320/824/1,152 ms at 5k, and 597/1,969/3,055 ms at 10k. The original run did not record CPU model or RAM, so it is kept as historical context only and is not combined with the current memory measurements. Its results were limited by the echo agent's 40 ms inter-chunk delay and Windows accept-queue pressure.
