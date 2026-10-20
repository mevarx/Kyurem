# ADR-0001: Go workspace layout and module paths

- Status: accepted
- Date: 2026-10-20
- Deciders: Kyurem maintainers
- Tags: build, structure

## Context

Kyurem spans several deployables and toolchains, and PRD §15 demands a
milestone-by-milestone build with an official A2A SDK dependency and strict
hot-path discipline. Go 1.26 workspaces support multi-module workspaces
natively, but we must decide now how many modules exist, whose dependency
set they fetch, and what the import paths look like for a project that will
be released and imported by others (client SDKs land in later milestones).

## Decision

The repository is a **Go workspace** (`go.work`) with **one module per
deployable boundary**:

```
kyurem/
├── go.work                  # workspaces ./gateway and ./demo
├── gateway/                 # module github.com/kyurem-project/kyurem/gateway
│   ├── cmd/kyurem/          # single CLI binary: serve | validate | version
│   └── internal/            # not importable outside the module
│       ├── config/ logging/ version/    (platform layer, no business logic)
│       ├── registry/ metrics/ proxy/    (data path)
│       └── server/                      (wiring, listeners, shutdown)
├── demo/                    # module github.com/kyurem-project/kyurem/demo
│   ├── agents/…             # example A2A agents (SDK usage reference)
│   └── scenarios/…          # end-to-end scripts + CLI
├── dashboard/               # TS module (lands in M3)
└── docs/adr/                # this file series
```

- **Internal packages** live under `gateway/internal/`: the Go compiler
  enforces the boundary the PRD asks for (no external reuse, no accidental
  coupling).
- **`test/` hierarchy inside `gateway/`** holds cross-package integration
  tests (`test/integration`); unit tests stay next to the code they test.
- The **`demo/` module is deliberately separate** from `gateway/` so demo
  binaries never affect the gateway's dependency graph or binary size; it
  still resolves `a2a-go` through the workspace.
- Module path is `github.com/kyurem-project/kyurem/<module>` so modules can
  be imported and tagged independently when the project releases.

## Consequences

- `go build ./gateway/...` and `go test ./gateway/...` are the primary CI
  units; `go.work` keeps multi-module commands like `go build ./...` working
  from the root for fast iteration.
- The gateway may not import `demo/`; the integration tests re-declare the
  echo/upper agent contracts locally instead.
- Adding a module (e.g. dashboard) means one workspace entry; nothing else
  moves.

## Alternatives considered

- **Single module** (`github.com/kyurem-project/kyurem`): simplest, but the
  demo and future language SDKs would share the gateway's dependency tree,
  and `internal/` boundaries would be unenforceable across products.
- **Per-package modules**: over-coupling to tooling; adds publish overhead
  for no internal-boundary benefit.
