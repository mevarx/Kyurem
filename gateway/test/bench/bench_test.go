// Micro benchmarks for the proxy hot path (M4, PRD §15: run with -benchmem
// like every Go benchmark in the docs).
//
// Every benchmark drives the real handler stack end to end over loopback
// HTTP (httptest servers), so the numbers include the mux-level routing
// (`resolve` over mixed paths), the card cache, ReverseProxy forwarding
// and the telemetry span lifecycle — the same shape the tiered load runs
// measure. They are skipped under `go test -short` (CI smoke) and never
// touch anything outside the current process.
package bench

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

// benchClient is the pooled client the micro benchmarks share.
func benchClient() *http.Client { return tunedClient(64, false) }

// benchCounters are the byte/event counters requestOnce wants; the micro
// benchmarks never check them, they only exist so the measured call is the
// exact call the load generator makes.
func benchCounters() (a1, a2, a3 atomic.Int64) { return }

// skipShort skips the benchmark under `-short` so the CI smoke run stays
// fast; benchmarks are for published numbers, not the build gate.
func skipShort(b *testing.B) {
	if testing.Short() {
		b.Skip("benchmarks are skipped under -short (run without -short; see docs/benchmarks.md)")
	}
}

// BenchmarkSendMessage forwards a message/send JSON-RPC round trip through
// the whole handler: routing + card cache hit + ReverseProxy streaming +
// span end + metrics. This is the per-request cost that `loadgen --mode
// send` reports at scale.
func BenchmarkSendMessage(b *testing.B) {
	skipShort(b)
	stack := startStack(b, 1024)
	client := benchClient()
	sent, recv, events := benchCounters()

	const text = "load bench ping"
	round := func() error {
		out := requestOnce(context.Background(), client, stack.route(), ModeSend, text, 0,
			&sent, &recv, &events)
		if !out.ok {
			return fmt.Errorf("%s", out.kind)
		}
		return nil
	}
	for i := 0; i < 3; i++ { // warm card cache and connection pool
		if err := round(); err != nil {
			b.Fatalf("warmup send: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := round(); err != nil {
			b.Fatalf("send round trip failed: %v", err)
		}
	}
}

// BenchmarkStreamMessage forwards a full message/stream SSE lifecycle —
// task submitted → artifact event per word → completed — the exact GW-2
// streaming passthrough shape the tiered runs saturate.
func BenchmarkStreamMessage(b *testing.B) {
	skipShort(b)
	stack := startStack(b, 1024)
	client := benchClient()
	sent, recv, events := benchCounters()

	const text = "load bench ping"
	round := func() error {
		out := requestOnce(context.Background(), client, stack.route(), ModeStream, text, 0,
			&sent, &recv, &events)
		if !out.ok {
			return fmt.Errorf("%s", out.kind)
		}
		return nil
	}
	for i := 0; i < 3; i++ {
		if err := round(); err != nil {
			b.Fatalf("warmup stream: %v", err)
		}
	}
	before := events.Load()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := round(); err != nil {
			b.Fatalf("stream round trip failed: %v", err)
		}
	}
	if after := events.Load(); after < before+int64(b.N) {
		b.Fatalf("event counter did not advance one full lifecycle per op: %d -> %d", before, after)
	}
}

// BenchmarkGatewayCardRoute serves the rewritten agent card with a warm
// cache: the response never touches the upstream, so the table isolates
// resolve + cache + card rewrite + JSON write.
func BenchmarkGatewayCardRoute(b *testing.B) {
	skipShort(b)
	stack := startStack(b, 1024)
	client := benchClient()
	cardURL := stack.route() + "/.well-known/agent-card.json"

	// Probe realistically: drain the body and require 200.
	getSize := func() (int, error) {
		resp, err := client.Get(cardURL)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close() //nolint:revive // per-iteration body
		n, _ := io.Copy(io.Discard, resp.Body)
		return int(n), nil
	}
	var warmSize int
	var warmErr error
	for i := 0; i < 3; i++ {
		warmSize, warmErr = getSize()
		if warmErr != nil {
			b.Fatalf("warmup card fetch: %v", warmErr)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		got, err := getSize()
		if err != nil {
			b.Fatalf("card serve failed: %v", err)
		}
		if got != warmSize {
			b.Fatalf("card size changed (%d -> %d)", got, warmSize)
		}
	}
}

// BenchmarkGatewayUnknownRoute is the routing-miss path: resolve walks the
// (short) ordered route list and fails closed with 404 — no upstream round
// trip, which is exactly the "mixed paths" branch of the resolve work.
func BenchmarkGatewayUnknownRoute(b *testing.B) {
	skipShort(b)
	stack := startStack(b, 1024)
	client := benchClient()

	get := func() (int, error) {
		resp, err := client.Get(stack.gw.URL + "/a2a/does-not-exist")
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close() //nolint:revive // per-iteration body
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	for i := 0; i < 3; i++ {
		if _, err := get(); err != nil {
			b.Fatalf("warmup unknown route: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		code, err := get()
		if err != nil {
			b.Fatalf("unknown-route GET failed: %v", err)
		}
		if code != http.StatusNotFound {
			b.Fatalf("unknown route answered HTTP %d, want 404", code)
		}
	}
}

// BenchmarkRequestEnvelope pins the per-request body cost (an allocation
// regression here shows up directly in p99 at the 10k tier).
func BenchmarkRequestEnvelope(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		body := envelope(methodStream, "load test ping", i)
		if len(body) == 0 {
			b.Fatalf("empty envelope")
		}
	}
}

// BenchmarkScrapeExposition pins the /metrics parser the scripts rely on
// (sample exposition mirrors the gateway collector set).
func BenchmarkScrapeExposition(b *testing.B) {
	exposition := []byte(`# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 42
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes 68198400
# TYPE process_cpu_seconds_total counter
process_cpu_seconds_total 1.25
# TYPE kyurem_proxy_active_streams gauge
kyurem_proxy_active_streams{agent="echo"} 3
# TYPE kyurem_proxy_requests_total counter
kyurem_proxy_requests_total{agent="echo",binding="JSONRPC",code="2xx"} 911
kyurem_proxy_requests_total{agent="echo",binding="JSONRPC",code="5xx"} 7
# TYPE kyurem_proxy_bytes_total counter
kyurem_proxy_bytes_total{agent="echo",direction="in"} 2048
kyurem_proxy_bytes_total{agent="echo",direction="out"} 8192
`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := scrape(exposition)
		if s.Goroutines != 42 || s.ProxyRequestsTotal != 918 || s.ProxyBytesOut != 8192 {
			b.Fatalf("bad scrape: %+v", s)
		}
	}
}
