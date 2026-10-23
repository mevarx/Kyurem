# Kyurem

**An A2A gateway for routing, tracing, policy, and resilient agent calls.** Kyurem transparently proxies A2A agents and provides a management API and dashboard for diagnosing their work.

[![Gateway CI](https://github.com/mevarx/Kyurem/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mevarx/Kyurem/actions/workflows/ci.yml)
[![A2A conformance](https://github.com/mevarx/Kyurem/actions/workflows/a2a-conformance.yml/badge.svg?branch=main)](https://github.com/mevarx/Kyurem/actions/workflows/a2a-conformance.yml)
[![Python demos](https://github.com/mevarx/Kyurem/actions/workflows/python-demo.yml/badge.svg?branch=main)](https://github.com/mevarx/Kyurem/actions/workflows/python-demo.yml)

## Dashboard

The task detail view exposes the retry and failover events from the scripted M6 walkthrough.

![Kyurem dashboard task detail showing successful failover and retry events](docs/images/dashboard-failover.png)

## Architecture

![Kyurem architecture](docs/images/architecture.svg)

## Capabilities

- **A2A proxy:** route by agent name or alias, forward JSON-RPC and HTTP+JSON, stream SSE, rewrite/cache agent cards, propagate W3C trace context, and drain on shutdown.
- **Tracing and accounting:** create a span per handoff, link task traces, export asynchronously through bounded OTLP queues, and report cost only when token usage is known.
- **Management and dashboard:** provide a separately bound API and views for tasks, agent graphs, costs, policy decisions, and span events.
- **Trust and policy:** authenticate bearer or mTLS callers, verify cards against operator-configured keys, apply deny-overrides rules, and retain bounded audit records. Skill filters are warn-only because A2A has no authoritative selected-skill field.
- **Failure handling:** retry within a bounded budget, fail over between same-binding upstreams, and isolate unhealthy upstreams with circuit breakers. Retries require safe methods or an idempotency-keyed, bounded POST; the upstream must deduplicate the key.
- **Python demonstrations:** the SDK-based echo and upper agents plus a deterministic fail-once agent show the retry path without side effects.

## Quick start

Requirements: Go 1.26+. Build the gateway and Go demo agents:

```sh
make build build-demo
make run-demo
```

For Python agents and the dashboard failover walkthrough, install Python 3.11+ and `uv`; Node 22 is needed for the UI:

```sh
make python-install
make dashboard-install
make demo-m6
```

The walkthrough launches the Python flaky and healthy agents, Kyurem, and the dashboard, posts one keyed task, and prints its task-detail URL. It keeps the services running until Ctrl+C. Use `make demo-m6-check` for the same backend assertions with automatic cleanup.

## Interfaces and configuration

- A2A data listener: `127.0.0.1:8080`
- Health, readiness, metrics, and version listener: `127.0.0.1:8081`
- Management API: `127.0.0.1:8082`
- Dashboard: `dashboard/`; development proxy to management API is configurable with `KYUREM_API_ORIGIN`.
- Strict YAML configuration rejects unknown keys and validates at startup. See [`config/kyurem.example.yaml`](config/kyurem.example.yaml), [`config/kyurem.compose.yaml`](config/kyurem.compose.yaml), and [`docs/configuration.md`](docs/configuration.md).

The sample configuration is for local development; authentication, trust verification, and policy enforcement are off by default. Configure production credentials, keys, and policy before exposing the gateway.

## A2A conformance in CI

The [A2A Conformance workflow](.github/workflows/a2a-conformance.yml) pins the official [`a2a-tck`](https://github.com/a2aproject/a2a-tck) and [`a2a-itk`](https://github.com/a2aproject/a2a-itk) repositories by commit. It runs the TCK MUST-level JSON-RPC suite against Kyurem with the Python demo upstream, then runs the ITK ACTS smoke profile for the Python demo’s declared task and discovery behaviors. Full ACTS coverage spans capabilities the small demo does not claim; the CI profile is intentionally scoped. Reports are retained as workflow artifacts.

## Benchmarks

Real-process streaming runs were collected on **Linux x86_64 in a Modal sandbox configured for 2 vCPU and 8 GiB RAM**, with **Go 1.26.0**. The virtual CPU model was not exposed. Gateway RSS is the process resident set size before and after each measurement interval; load generation and the Go echo agent ran on the same host. These are measurements of the checked-in harness, not an SLA.

| Configured streams | Completed / failed | p50 / p95 / p99 latency (ms) | Peak active streams | Gateway RSS before → after (Δ MiB) | Throughput (req/s) |
|---:|---:|---:|---:|---:|---:|
| 1,000 | 5,015 / 0 | 3,109 / 7,159 / 9,039 | 235 | 54.0 → 231.2 (+177.2) | 245.0 |
| 5,000 | 5,697 / 0 | 13,289 / 24,702 / 26,048 | 282 | 231.2 → 1,022.9 (+791.7) | 184.6 |
| 10,000 | 847 / 10,555 | 19,997 / 28,822 / 29,287 | 22 | 1,022.9 → 1,342.4 (+319.6) | 25.6 |

The 10,000-stream tier did **not** sustain its target on this 2-vCPU test host; its latency summarizes completed requests during an overloaded run, not a successful 10k-capacity result. The script saves raw JSON and logs under `demo/bin/bench-results/` (`.gitignore`d). Run it with `bash demo/scenarios/bench-load.sh`; see [`docs/benchmarks.md`](docs/benchmarks.md) for the in-process smoke and measurement details.

## Releases

The tag-triggered release workflow builds Kyurem binaries for Linux, macOS, and Windows (amd64/arm64 where supported), and publishes multi-architecture Linux/amd64 and Linux/arm64 images for the gateway, dashboard, Go demos, and Python demos to GHCR:

- `ghcr.io/mevarx/kyurem-gateway`
- `ghcr.io/mevarx/kyurem-dashboard`
- `ghcr.io/mevarx/kyurem-demo`
- `ghcr.io/mevarx/kyurem-python-demo`

After this change is merged, tagging `v0.1.0` starts the first release build; the tag is not created from an unmerged feature branch.

## Development checks

```sh
make test            # Gateway Go tests with the race detector
make test-demo       # Go demo tests
make test-python     # Python demo agent tests
make vet
make validate
make dashboard-check
make dashboard-build
make demo-m3          # failure diagnosis via management API
```

Compose syntax is validated in GitHub Actions. Running the full telemetry/ClickHouse stack locally requires Docker. The official TCK/ACTS job and larger benchmark tiers are described above and in [`docs/milestones.md`](docs/milestones.md).
