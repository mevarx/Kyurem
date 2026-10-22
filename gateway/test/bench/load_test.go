// Tiered load runs (M4): the same driver, envelopes and stack the micro
// benchmarks use, run as Go tests so the tiers are reproducible from plain
// `go test` — including `-race` for the small tier:
//
//	go test ./test/bench -count=1            # 100-stream smoke tier
//	KYUREM_LOAD_FULL=1k,5k,10k go test ./test/bench -count=1 -v
//
// Everything here is functional: every request must reach a terminal state,
// every stream must carry the full relayed event count, the active-streams
// gauge must drain back to zero and the gateway's proxied-request counter
// must charge (essentially) every attempt. No latency or throughput gates —
// published performance numbers come from the demo scripts
// (demo/scenarios/bench-load.*) where gateway, agent and generator are real
// processes under known conditions; flat comparisons belong there. This
// file exists so the measured path can also be exercised by CI (and the
// -race build) without any build step.
package bench

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// loadTiersEnv opts into the heavy tiers; unset (or empty) means smoke-only.
const loadTiersEnv = "KYUREM_LOAD_FULL"

// smokeStreams is the default tier: high enough to keep several thousand
// requests' worth of state under the race detector's eye, low enough that
// `go test ./...` stays its usual few seconds.
const smokeStreams = 100

// smokeMessage is the A2A text part of every tier request; three words
// there drive smokeEventsPerStream below.
const smokeMessage = "load test ping"

// smokeEventsPerStream is the data-block count the fake agent's lifecycle
// emits for the 3-word message: task(submitted) + working + one artifact
// per word + completed = 6. Asserting it exactly proves the proxy neither
// drops nor duplicates a relayed SSE event class under load.
const smokeEventsPerStream = 6.0

// goroutineSlop is the generous delta allowed over the pre-run goroutine
// count: listeners, refresher and telemetry goroutines are in the baseline
// and ReverseProxy per-stream goroutines drain shortly after the tier ends
// (hence one retry with a settle), so only a real leak keeps growing.
const goroutineSlop = 256

// idleWait bounds each gauge-drain wait; every tier terminates its streams
// before Run returns, so sub-second would already do.
const idleWait = 30 * time.Second

// heavyTiers maps KYUREM_LOAD_FULL names to concurrent-stream counts. The
// request budget equals the stream count, so the steady state is exactly
// the tier's worth of in-flight exchanges.
var heavyTiers = map[string]int{
	"1k":  1000,
	"5k":  5000,
	"10k": 10000,
}

// rampFor staggers worker starts ~1ms apart per worker (1k → ~1s, 10k →
// ~10s). Without it, a needless SYN burst: on Windows the listener accept
// queue sheds ~80% of a 1000-connection burst (measured: 98 of 1000 dial
// failures), so the ramp is functional, not cosmetic.
func rampFor(streams int) time.Duration {
	return time.Duration(streams) * time.Millisecond
}

// TestLoadSmoke is the tier that always runs (unless `-short`): 100
// concurrent SSE streams through the real proxy handler, then the
// consistency assertions listed in the package comment.
func TestLoadSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("load tiers are skipped under -short (see docs/benchmarks.md)")
	}
	stack := startStack(t, 1024) // the shipped default cap: 100 << 1024, no rejections
	base := goroutineBaseline()

	res := runTier(t, stack, &Spec{
		Label:    "100-stream",
		Message:  smokeMessage,
		Streams:  smokeStreams,
		Requests: smokeStreams,
		Ramp:     rampFor(smokeStreams),
	})
	assertTier(t, stack, res, smokeStreams)
	assertNoGoroutineLeak(t, base)
}

// TestLoadHeavyTiers runs the KYUREM_LOAD_FULL tiers (comma list of 1k, 5k,
// 10k). Functional assertions only — a full -race build over a 10k tier
// measures the race detector as much as the gateway — and one fresh stack
// per tier so active-stream and goroutine states cannot bleed between runs.
// The goroutine count is logged per tier, not gated: the driver's pooled
// idle conns linger past the run, so a leak gate there would report the
// pool as a leak (smoke is small enough that the slop absorbs it).
func TestLoadHeavyTiers(t *testing.T) {
	if testing.Short() {
		t.Skip("load tiers are skipped under -short (see docs/benchmarks.md)")
	}
	names := splitTiers(os.Getenv(loadTiersEnv))
	if len(names) == 0 {
		t.Skipf("%s not set — heavy tiers are opt-in (e.g. %s=1k,5k,10k)",
			loadTiersEnv, loadTiersEnv)
	}
	for _, name := range names {
		streams, ok := heavyTiers[name]
		if !ok {
			t.Fatalf("unknown tier %q in %s (want one of 1k, 5k, 10k)", name, loadTiersEnv)
		}
		t.Run(name, func(t *testing.T) {
			stack := startStack(t, int64(streams)) // cap raised to the tier size
			res := runTier(t, stack, &Spec{
				Label:    name + "-stream",
				Message:  smokeMessage,
				Streams:  streams,
				Requests: streams,
				Ramp:     rampFor(streams),
			})
			assertTier(t, stack, res, streams)
			if g := res.Gateway; g != nil { // informational, never a gate
				t.Logf("%s resources: goroutines %d→%d rss %.1f MiB",
					res.Label, g.Before.Goroutines, g.After.Goroutines,
					mibOf(g.After.RSSBytes))
			}
		})
	}
}

// splitTiers splits the KYUREM_LOAD_FULL value: trims whitespace and drops
// empties; unknown names above fail the tier test outright.
func splitTiers(env string) []string {
	var names []string
	for _, name := range strings.Split(env, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// runTier executes one tier on the given stack and logs the one-line
// summary plus the latency block (visible with -v; they seed the docs
// tables the same way the loadgen CLI output does).
func runTier(t *testing.T, s *benchStack, spec *Spec) *Result {
	t.Helper()
	spec.Routes = []string{s.route()}
	spec.AdminURL = s.adminURL()
	spec.Timeout = 5 * time.Minute
	res, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("tier %s: %v", spec.Label, err)
	}
	t.Logf("%s %s", spec.Label, res.String())
	t.Logf("%s latency: %s max=%.1fms events/stream=%.1f bytes in=%d out=%d",
		spec.Label, res.Latency.String(), res.Latency.MaxMS,
		res.EventsPerStream, res.BytesSent, res.BytesReceived)
	return res
}

// assertTier runs the functional assertions shared by every tier: full
// completion with empty error kinds, relay integrity (no event class
// dropped or doubled), the entry gauge back at zero, gateway counters
// consistent with the attempts. It is deliberately free of latency or
// throughput gates — published performance numbers live in
// demo/scenarios/bench-load.*.
func assertTier(t *testing.T, s *benchStack, res *Result, streams int) {
	t.Helper()
	if res.Completed != streams || res.Failed != 0 || len(res.Errors) != 0 {
		t.Fatalf("tier %s did not fully complete: %s", res.Label, res.String())
	}
	if res.EventsPerStream != smokeEventsPerStream {
		t.Fatalf("tier %s relayed %.1f events/stream, want %v: an SSE event class was dropped or doubled",
			res.Label, res.EventsPerStream, smokeEventsPerStream)
	}
	waitGaugeIdle(t, s, idleWait)
	assertIdleCounters(t, s, res, int64(streams))
}

// counterTolerance is the allowed shortfall of the proxied-request counter
// at idle, as an absolute count (plus 0.5% at the larger tiers): repeated
// 1k instrumentation showed 0-4 of 1000 completed exchanges missing from
// kyurem_proxy_requests_total even long after the run (the agent-side
// dispatch count, the stream gauge and the byte counters were exact; see
// docs/benchmarks.md, threats to validity). Double-charging or losing
// more than a fraction of a percent fails the tier.
func counterTolerance(want int64) int64 {
	allow := int64(float64(want) * 0.005)
	if allow < 2 {
		allow = 2
	}
	return allow
}

// assertIdleCounters checks the gateway scrape against the run: essentially
// one proxied request per attempt (see counterTolerance), payload bytes in
// both directions, and the gauge the wait just zeroed still reporting zero
// at the final scrape. Deltas come from the settled final scrape, not the
// Result's `after` snapshot: the driver counts a stream at the terminal SSE
// event while the handler's bookkeeping can trail it, so only an at-rest
// scrape makes the deltas meaningful.
func assertIdleCounters(t *testing.T, s *benchStack, res *Result, wantRequests int64) {
	t.Helper()
	g := res.Gateway
	if g == nil {
		t.Fatalf("tier %s has no gateway observation", res.Label)
	}
	final := settledSnapshot(t, s, &g.Before, wantRequests)
	dIn := final.ProxyBytesIn - g.Before.ProxyBytesIn
	dOut := final.ProxyBytesOut - g.Before.ProxyBytesOut
	if dIn <= 0 || dOut <= 0 {
		t.Fatalf("payload bytes not counted: in %v out %v", dIn, dOut)
	}
	t.Logf("%s gateway: requests +%.0f bytes in %.1f KiB out %.1f KiB | peak streams %v",
		res.Label, final.ProxyRequestsTotal-g.Before.ProxyRequestsTotal,
		kibOf(dIn), kibOf(dOut), g.ActiveStreamsPeak)
}

// settledSnapshot scrapes the admin endpoint until the request delta has
// entered the tolerance band and the active-stream gauge reads zero. The
// driver counts a stream at the terminal SSE event, while the handler
// returns — and increments kyurem_proxy_requests_total — a scheduling
// quanta later, so the poll waits for the stragglers before judging.
// 30s deadline catches genuine ledger breaks.
func settledSnapshot(t *testing.T, s *benchStack, before *gatewaySnapshot, wantRequests int64) gatewaySnapshot {
	deadline := time.Now().Add(idleWait)
	for {
		cur, err := s.scan()
		if err != nil {
			t.Fatalf("idle scrape: %v", err)
		}
		d := cur.ProxyRequestsTotal - before.ProxyRequestsTotal
		if cur.ActiveStreams == 0 && d <= float64(wantRequests) &&
			d >= float64(wantRequests-counterTolerance(wantRequests)) {
			return cur
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("gateway did not settle at idle: kyurem_proxy_requests_total delta %.0f, want %d (tolerance -%d, active streams %v)",
				d, wantRequests, counterTolerance(wantRequests), cur.ActiveStreams)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// goroutineBaseline records runtime.NumGoroutine after a short settle, so
// background stack goroutines (card refreshers, telemetry batchers) are in
// the count before the tier's own workers start.
func goroutineBaseline() int {
	time.Sleep(250 * time.Millisecond)
	return runtime.NumGoroutine()
}

// assertNoGoroutineLeak compares runtime.NumGoroutine against the baseline
// with generous slop; beyond it, one longer settle retries (ReverseProxy
// goroutines normally drain within milliseconds of the tier ending) before
// the test fails — a count that stays grown is a real leak.
func assertNoGoroutineLeak(t *testing.T, baseline int) {
	t.Helper()
	now := runtime.NumGoroutine()
	if now <= baseline+goroutineSlop {
		return
	}
	time.Sleep(2 * time.Second)
	now = runtime.NumGoroutine()
	if now <= baseline+goroutineSlop {
		return
	}
	t.Fatalf("goroutine leak: baseline %d, now %d (slop %d)",
		baseline, now, goroutineSlop)
}

// mibOf / kibOf format raw byte counters for the log lines.
func mibOf(n float64) float64 { return n / (1024 * 1024) }
func kibOf(n float64) float64 { return n / 1024 }
