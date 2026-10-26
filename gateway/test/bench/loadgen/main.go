// kyurem-loadgen (M4 load generator).
//
// Opens N concurrent A2A exchanges against a Kyurem gateway route and
// measures latency, throughput and errors while scraping the gateway's
// admin Prometheus endpoint for resource counters (goroutines, resident
// memory, CPU seconds, active streams, proxied request/byte counters).
//
// It speaks the raw A2A v1.0 wire — POST JS-NRPC `SendMessage`
// (request/response) or `SendStreamingMessage` (SSE) with an SDK-shaped
// envelope, then parses `data:` blocks until the task reaches a terminal
// state. No SDK client machinery is involved, so the numbers reflect the
// wire and the gateway, not client-side bookkeeping.
//
// Typical invocation (see docs/benchmarks.md and demo/scenarios/bench-load.*):
//
//	kyurem-loadgen --gateway-url http://127.0.0.1:18080/a2a/echo \
//	  --admin-url http://127.0.0.1:18081 --mode stream \
//	  --streams 1000 --requests 1000 --ramp 2s --label 1k-stream --output json
//
// Exit code: 0 when every measured request reached its terminal state, 1
// otherwise (failures print with their kind breakdown). Progress lines go
// to stderr every second so stdout stays parseable in --output json mode.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	bench "github.com/kyurem-project/kyurem/gateway/test/bench"
)

func main() {
	var opts loadSpec
	flag.StringVar(&opts.GatewayURL, "gateway-url", "http://127.0.0.1:8080/a2a/echo",
		"gateway agent route for the single-route case")
	flag.StringVar(&opts.Agents, "agents", "",
		"comma-separated gateway routes to spread load over (overrides --gateway-url)")
	flag.StringVar(&opts.AdminURL, "admin-url", "http://127.0.0.1:8081",
		"gateway admin listener for metrics/version scraping ('' skips scraping)")
	flag.StringVar(&opts.Mode, "mode", "stream",
		"A2A exchange shape: `stream` (message/stream + SSE) or `send` (message/send)")
	flag.IntVar(&opts.Streams, "streams", 100, "concurrent workers/streams")
	flag.IntVar(&opts.Requests, "requests", 0,
		"total request budget (0 = unbounded; then --duration is required)")
	flag.DurationVar(&opts.Duration, "duration", 0,
		"time budget; whichever of --requests/--duration expires first ends the run")
	flag.DurationVar(&opts.Ramp, "ramp", 0, "stagger worker starts over this window")
	flag.DurationVar(&opts.Timeout, "timeout", 60*time.Second, "per-request deadline")
	flag.IntVar(&opts.Retries, "retries", 2,
		"per-request retries for transient failures (connect/reset/5xx)")
	flag.StringVar(&opts.Message, "message", "load test ping",
		"A2A text part (the demo echo agent streams one artifact per word)")
	flag.StringVar(&opts.Label, "label", "", "tier label recorded in the JSON result")
	flag.StringVar(&opts.Output, "output", "text", "output format: `text` or `json`")
	flag.BoolVar(&opts.KeepAlive, "keepalive", true,
		"keep the client transport's connection pooling (off = TCP handshake per request)")
	flag.DurationVar(&opts.DrainTimeout, "drain-timeout", 30*time.Second,
		"after the run, wait this long for the gateway's active streams to reach zero")
	flag.BoolVar(&opts.RequirePeak, "require-peak", false,
		"fail unless the gateway's active-streams peak reached --streams (proves the concurrency was held)")
	flag.IntVar(&opts.AllocatePorts, "allocate-ports", 0,
		"bind N loopback ports (to hand to gateway/agents), print them one per line, and exit")
	flag.Parse()
	os.Exit(opts.run())
}

// loadSpec bundles the parsed flags into one place.
type loadSpec struct {
	GatewayURL    string
	Agents        string
	AdminURL      string
	Mode          string
	Streams       int
	Requests      int
	Duration      time.Duration
	Ramp          time.Duration
	Timeout       time.Duration
	Retries       int
	Message       string
	Label         string
	Output        string
	KeepAlive     bool
	AllocatePorts int
	DrainTimeout  time.Duration
	RequirePeak   bool
}

// run is main's body returning the process exit code.
func (l *loadSpec) run() int {
	if l.AllocatePorts > 0 {
		ports, err := allocatePorts(l.AllocatePorts)
		if err != nil {
			note(fmt.Sprintf("allocate ports: %v", err))
			return 1
		}
		for _, p := range ports {
			fmt.Println(p)
		}
		return 0
	}
	routes := splitRoutes(l.Agents, l.GatewayURL)
	if l.Output != "json" && l.Output != "text" {
		note(fmt.Sprintf("unknown --output %q (want text|json)", l.Output))
		return 1
	}
	if l.Requests == 0 && l.Duration == 0 {
		// Ergonomics: neither budget set would run forever; default one.
		l.Duration = 30 * time.Second
	}
	spec := &bench.Spec{
		Label:             l.Label,
		Routes:            routes,
		AdminURL:          l.AdminURL,
		Mode:              bench.Mode(l.Mode),
		Message:           l.Message,
		Streams:           l.Streams,
		Requests:          l.Requests,
		Duration:          l.Duration,
		Ramp:              l.Ramp,
		Timeout:           l.Timeout,
		Retries:           l.Retries,
		DrainTimeout:      l.DrainTimeout,
		Progress:          os.Stderr,
		DisableKeepAlives: !l.KeepAlive,
	}

	// Ctrl-C: let Run run to completion of the drain it already started
	// (the context cancel just stops new work; partial numbers stay true).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	res, err := bench.Run(ctx, spec)
	if err != nil {
		note(err.Error())
		return 1
	}
	switch l.Output {
	case "json":
		raw, jerr := json.MarshalIndent(res, "", "  ")
		if jerr != nil {
			note(fmt.Sprintf("encode result: %v", jerr))
			return 1
		}
		fmt.Println(string(raw))
	default:
		fmt.Fprint(os.Stdout, human(res))
	}
	if res.Failed > 0 {
		note(fmt.Sprintf("FAILED: %d request(s) failed to reach a terminal state", res.Failed))
		return 1
	}
	if g := res.Gateway; g != nil && l.AdminURL != "" {
		if !g.Drained {
			note(fmt.Sprintf("FAILED: gateway still reports %.0f active stream(s) %.0fs after the run",
				g.After.ActiveStreams, g.DrainSeconds))
			return 1
		}
		if l.RequirePeak && g.ActiveStreamsPeak < float64(l.Streams) {
			note(fmt.Sprintf("FAILED: gateway active-streams peak %.0f never reached the %d requested streams",
				g.ActiveStreamsPeak, l.Streams))
			return 1
		}
	}
	return 0
}

// note writes diagnostics (all non-result output) to stderr so stdout stays
// parseable in json mode.
func note(msg string) {
	fmt.Fprintln(os.Stderr, msg)
}

// allocatePorts binds n short-lived loopback listeners so the benchmark
// scripts never collide with other local services (or with themselves on a
// second run). Ports are small integers; local races after release are
// tolerated by the caller re-probing health.
func allocatePorts(n int) ([]int, error) {
	ports := make([]int, 0, n)
	seen := map[int]bool{}
	for len(ports) < n {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		if seen[port] {
			continue
		}
		seen[port] = true
		ports = append(ports, port)
	}
	return ports, nil
}

// human renders the text summary as a few labeled lines.
func human(r *bench.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "label=%s mode=%s streams=%d wall=%.2fs\n",
		r.Label, r.Mode, r.Streams, r.WallSeconds)
	fmt.Fprintf(&b, "completed=%d failed=%d retried=%d attempts=%d\n",
		r.Completed, r.Failed, r.Retried, r.Attempts)
	fmt.Fprintf(&b, "latency: %s max=%.1fms\n", r.Latency.String(), r.Latency.MaxMS)
	fmt.Fprintf(&b, "throughput=%.1f rps events/stream=%.1f bytes sent=%d received=%d\n",
		r.ThroughputRPS, r.EventsPerStream, r.BytesSent, r.BytesReceived)
	if g := r.Gateway; g != nil {
		if g.ScrapeFailed != "" {
			fmt.Fprintf(&b, "gateway: scraping incomplete (%s)\n", g.ScrapeFailed)
		} else {
			fmt.Fprintf(&b, "gateway: goroutines %d→%d | rss %.1f→%.1f MiB | cpu %.2f→%.2fs\n",
				g.Before.Goroutines, g.After.Goroutines,
				mib(g.Before.RSSBytes), mib(g.After.RSSBytes),
				g.Before.CPUSeconds, g.After.CPUSeconds)
			fmt.Fprintf(&b,
				"gateway: active streams %v→%v peak %v | proxied requests +%.0f | bytes in %.1f MiB out %.1f MiB\n",
				g.Before.ActiveStreams, g.After.ActiveStreams, g.ActiveStreamsPeak,
				g.ProxyRequestsDelta(), mib(g.ProxyBytesInDelta()), mib(g.ProxyBytesOutDelta()))
			if v := g.Versions; v != nil {
				fmt.Fprintf(&b, "gateway versions: kyurem=%s a2a_spec=%s a2a_sdk=%s\n",
					v.KyuremVersion, v.A2ASpecVersion, v.A2AGoSDKVersion)
			}
		}
	}
	return b.String()
}

// mib formats bytes as Mebibytes for the text summary.
func mib(bytes float64) float64 { return bytes / (1024 * 1024) }

// splitRoutes turns --agents into the route list; an empty list falls back
// to --gateway-url (the default single-route case).
func splitRoutes(agents, gatewayURL string) []string {
	var out []string
	for _, r := range strings.Split(agents, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		out = []string{gatewayURL}
	}
	return out
}
