// Package bench is Kyurem's load-test tooling (M4): a concurrency driver
// that speaks raw A2A v1.0 JSON-RPC over HTTP — SendMessage round trips and
// SendStreamingMessage SSE streams — a latency/throughput/percentile
// recorder and a best-effort scrape of the gateway admin Prometheus
// endpoint for resource counters.
//
// Two consumers share this file:
//
//   - loadgen/main.go, the `kyurem-loadgen` CLI that the demo scenario
//     scripts (demo/scenarios/bench-load.*) drive against real gateway
//     processes, and
//   - the package's own tests (bench_test.go micro benchmarks, load_test.go
//     tiered load runs), so every measured path is the same path.
//
// SSE framing follows the format the a2a-go SDK emits on the A2A v1.0
// wire: each SSE data block is a JSON-RPC response whose `result` is the
// A2A event union (message | task | statusUpdate | artifactUpdate), and a
// stream is terminal when a status update reaches a terminal task state
// (submitted/working are not terminal; a2a-go v2 emits no `final` flag).
package bench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// JSON-RPC method names on the A2A v1.0 wire (a2a-go v2, internal/jsonrpc):
// the SDK transport POSTs these to the agent's interface URL, and the
// gateway forwards them verbatim.
const (
	methodSend   = "SendMessage"
	methodStream = "SendStreamingMessage"
	// sseAccept is the Accept header a streaming request sends.
	sseAccept = "text/event-stream"
)

// terminalTaskStates end an A2A stream (A2A v1.0 task lifecycle; states
// other than these keep the stream open).
var terminalTaskStates = map[string]bool{
	"TASK_STATE_COMPLETED": true,
	"TASK_STATE_FAILED":    true,
	"TASK_STATE_CANCELED":  true,
	"TASK_STATE_REJECTED":  true,
}

// Mode selects the A2A exchange shape.
type Mode string

const (
	// ModeStream is message/stream + SSE until the task state is terminal.
	ModeStream Mode = "stream"
	// ModeSend is message/send, a plain request/response round trip.
	ModeSend Mode = "send"
)

// Spec configures one load run.
type Spec struct {
	// Label lands verbatim in the Result (tier name, e.g. "1k-stream").
	Label string
	// Routes are the gateway agent route URLs to hit; load spreads round
	// robin across them (worker i uses Routes[i%len]). At least one.
	Routes []string
	// AdminURL is the gateway admin listener base used to scrape /metrics
	// and /version before, during (peak) and after the run. "" skips it.
	AdminURL string
	// Mode picks the exchange shape (default ModeStream).
	Mode Mode
	// Message is the A2A text part on every request. The demo go-echo
	// agent streams one artifact per word with a 40ms inter-chunk sleep,
	// so the word count drives per-stream event count and duration.
	Message string
	// Streams is the number of concurrent workers; each worker re-opens
	// requests back to back, so the steady state is Streams in-flight.
	Streams int
	// Requests is the total request budget (0 = unbounded, use Duration).
	Requests int
	// Duration is the time budget; the run ends when either budget runs
	// out first.
	Duration time.Duration
	// Ramp staggers worker starts over this window (0 = all at once).
	Ramp time.Duration
	// Timeout bounds one request end to end (0 = 5 minutes). Retries
	// share the same deadline, so a request cannot outlive it.
	Timeout time.Duration
	// Retries is the per-request retry budget for transient failures
	// (connect failures, connection resets, 5xx before response bytes).
	// Retries never mask protocol errors or timeouts; every failed
	// attempt is still counted in Result.Errors.
	Retries int
	// Progress, when set, receives one summary line per second (the CLI
	// wires os.Stderr).
	Progress io.Writer
	// HTTPClient overrides the tuned default client (tests inject one).
	HTTPClient *http.Client
	// DisableKeepAlives turns off connection pooling (each request pays a
	// TCP handshake). The benchmark defaults to pooling; the flag only
	// exists to quantify what pooling buys.
	DisableKeepAlives bool
}

// Stable error-kind names: the load tests, demo scripts, JSON output and
// docs key on them, so they must not drift.
const (
	ErrConnect   = "connect_error"            // dial or lookup failed
	ErrTransport = "transport_error"          // reset / broken pipe / early close
	ErrHTTP      = "http_status_"             // prefix + code, e.g. http_status_502
	ErrA2A       = "a2a_error"                // JSON-RPC error object
	ErrSSEEOF    = "sse_end_without_terminal" // stream ended before a terminal state
	ErrTimeout   = "timeout"
	ErrCanceled  = "canceled"           // run context ended mid-flight
	ErrBody      = "malformed_response" // not parseable as an A2A payload
)

// Latencies summarizes per-request latency (successful requests only;
// milliseconds from POST start to the last streamed/read body byte).
type Latencies struct {
	P50MS  float64 `json:"p50_ms"`
	P95MS  float64 `json:"p95_ms"`
	P99MS  float64 `json:"p99_ms"`
	P999MS float64 `json:"p999_ms"`
	MeanMS float64 `json:"mean_ms"`
	MaxMS  float64 `json:"max_ms"`
}

// String renders the percentile block for one-line tables.
func (l Latencies) String() string {
	return fmt.Sprintf("p50=%.1fms p95=%.1fms p99=%.1fms mean=%.1fms",
		l.P50MS, l.P95MS, l.P99MS, l.MeanMS)
}

// Result carries everything one load run measured. JSON tags are snake_case
// to match the management API style; documents are published verbatim in
// docs/benchmarks.md, so fields are additive-only by convention.
type Result struct {
	Label         string         `json:"label"`
	Mode          string         `json:"mode"`
	Message       string         `json:"message"`
	Routes        int            `json:"routes"`
	Streams       int            `json:"streams"`
	RequestBudget int            `json:"request_budget"`
	Started       time.Time      `json:"started_utc"`
	WallSeconds   float64        `json:"wall_seconds"`
	Completed     int            `json:"completed"`
	Failed        int            `json:"failed"`
	Retried       int            `json:"retried"`
	Attempts      int            `json:"attempts"`
	Errors        map[string]int `json:"errors"`
	// EventsPerStream is the mean number of SSE data blocks per completed
	// stream (0 in send mode). It sanity-checks stream length: the demo
	// go-echo agent emits 5 events for a 2-word message (submitted task,
	// working, artifact, artifact, completed).
	EventsPerStream float64   `json:"events_per_completed"`
	Latency         Latencies `json:"latency"`
	// ThroughputRPS is completed / wall seconds.
	ThroughputRPS float64 `json:"throughput_rps"`
	// BytesSent/BytesReceived are client-side counts: request bodies and
	// exactly the response bytes the driver read off the wire (compression
	// is disabled, so these are transfer bytes).
	BytesSent     int64               `json:"bytes_sent"`
	BytesReceived int64               `json:"bytes_received"`
	Gateway       *GatewayObservation `json:"gateway,omitempty"`
}

// String renders the one-line summary the demo scripts embed in tables.
func (r *Result) String() string {
	kinds := make([]string, 0, len(r.Errors))
	for k, n := range r.Errors {
		kinds = append(kinds, fmt.Sprintf("%s=%d", k, n))
	}
	sort.Strings(kinds)
	errPart := "{}"
	if len(kinds) > 0 {
		errPart = strings.Join(kinds, ",")
	}
	return fmt.Sprintf(
		"%s mode=%s streams=%d ok=%d fail=%d retried=%d {%s} rps=%.1f wall=%.2fs errors=%s",
		r.Label, r.Mode, r.Streams, r.Completed, r.Failed, r.Retried,
		r.Latency.String(), r.ThroughputRPS, r.WallSeconds, errPart)
}

// gatewaySnapshot is one point-in-time scrape of the gateway admin
// /metrics endpoint. A series missing from the exposition is not the same
// as a zero (platform collector differences), so unknown fields stay -1.
type gatewaySnapshot struct {
	Goroutines         int     `json:"go_goroutines"`
	RSSBytes           float64 `json:"resident_memory_bytes"`
	CPUSeconds         float64 `json:"cpu_seconds_total"`
	ActiveStreams      float64 `json:"active_streams"`
	ProxyRequestsTotal float64 `json:"proxy_requests_total"`
	ProxyBytesIn       float64 `json:"proxy_bytes_in_total"`
	ProxyBytesOut      float64 `json:"proxy_bytes_out_total"`
}

// scrape parses a Prometheus text exposition into a snapshot. Per-agent
// label series (kyurem_proxy_*) are summed; kyurem_proxy_bytes_total is
// additionally split by the `direction` label ("in"/"out").
func scrape(body []byte) gatewaySnapshot {
	out := gatewaySnapshot{
		Goroutines: -1,
		RSSBytes:   -1,
		CPUSeconds: -1,
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		sp := bytes.IndexByte(line, ' ')
		if sp <= 0 {
			continue
		}
		name := string(line[:sp])
		v, err := strconv.ParseFloat(strings.TrimSpace(string(line[sp:])), 64)
		if err != nil {
			continue
		}
		metric := name
		labels := ""
		if b := strings.IndexByte(metric, '{'); b >= 0 {
			labels, metric = metric[b:], metric[:b]
		}
		switch metric {
		case "go_goroutines":
			out.Goroutines = int(v)
		case "process_resident_memory_bytes":
			out.RSSBytes = v
		case "process_cpu_seconds_total":
			out.CPUSeconds = v
		case "kyurem_proxy_active_streams":
			out.ActiveStreams += v
		case "kyurem_proxy_requests_total":
			out.ProxyRequestsTotal += v
		case "kyurem_proxy_bytes_total":
			switch labelValue(labels, "direction") {
			case "out":
				out.ProxyBytesOut += v
			default:
				// "in" and any future direction default here.
				out.ProxyBytesIn += v
			}
		}
	}
	return out
}

// labelValue extracts one label value from a rendered label set (k="v").
func labelValue(labels, key string) string {
	want := key + `="`
	i := strings.Index(labels, want)
	if i < 0 {
		return ""
	}
	rest := labels[i+len(want):]
	if j := strings.Index(rest, `"`); j >= 0 {
		return rest[:j]
	}
	return ""
}

// VersionInfo mirrors the admin /version payload (result provenance).
type VersionInfo struct {
	KyuremVersion   string `json:"kyurem_version"`
	A2ASpecVersion  string `json:"a2a_spec_version"`
	A2AGoSDKVersion string `json:"a2a_go_sdk_version"`
}

// GatewayObservation compares snapshots taken around one run and adds the
// peak active-streams gauge (a gauge only holds "now", so run while
// polling) plus delta fields the demo scripts print. The raw snapshots
// stay in the published JSON so every number remains auditable.
type GatewayObservation struct {
	Before            gatewaySnapshot `json:"before"`
	After             gatewaySnapshot `json:"after"`
	ActiveStreamsPeak float64         `json:"active_streams_peak"`
	Versions          *VersionInfo    `json:"versions,omitempty"`
	ScrapeFailed      string          `json:"scrape_failed,omitempty"`
}

// Delta accessors used by the scripts / text renderer.
func (g *GatewayObservation) GoroutinesDelta() int { return g.After.Goroutines - g.Before.Goroutines }
func (g *GatewayObservation) RSSDelta() float64    { return g.After.RSSBytes - g.Before.RSSBytes }
func (g *GatewayObservation) CPUDelta() float64    { return g.After.CPUSeconds - g.Before.CPUSeconds }
func (g *GatewayObservation) ProxyRequestsDelta() float64 {
	return g.After.ProxyRequestsTotal - g.Before.ProxyRequestsTotal
}
func (g *GatewayObservation) ProxyBytesInDelta() float64 {
	return g.After.ProxyBytesIn - g.Before.ProxyBytesIn
}
func (g *GatewayObservation) ProxyBytesOutDelta() float64 {
	return g.After.ProxyBytesOut - g.Before.ProxyBytesOut
}
func (g *GatewayObservation) ActiveStreamsDelta() float64 {
	return g.After.ActiveStreams - g.Before.ActiveStreams
}

// scrapeAdmin reads one /metrics snapshot from the gateway admin listener.
func scrapeAdmin(ctx context.Context, adminURL string) (gatewaySnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(adminURL, "/")+"/metrics", nil)
	if err != nil {
		return gatewaySnapshot{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return gatewaySnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return gatewaySnapshot{}, fmt.Errorf("admin /metrics answered HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return gatewaySnapshot{}, err
	}
	return scrape(raw), nil
}

// fetchVersions reads the admin /version payload (best effort).
func fetchVersions(ctx context.Context, adminURL string) *VersionInfo {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(adminURL, "/")+"/version", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil
	}
	var v VersionInfo
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return &v
}

// tunedClient builds the load client for S concurrent workers. Defaults
// that starve many workers (MaxIdleConnsPerHost=2) are raised per host; an
// ambient proxy is stripped (loopback benches must bypass it), compression
// is disabled so SSE chunks flow through unchanged and byte counts are
// transfer bytes, and HTTP/2 is off — the dem o path is HTTP/1.1.
func tunedClient(streams int, disableKeepAlives bool) *http.Client {
	perHost := streams
	if perHost < 1024 {
		perHost = 1024
	}
	return &http.Client{
		// Per-request deadlines ride each request's context instead.
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          0, // unlimited
			MaxIdleConnsPerHost:   perHost,
			MaxConnsPerHost:       0, // unlimited
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			DisableCompression:    true,
			ForceAttemptHTTP2:     false,
			DisableKeepAlives:     disableKeepAlives,
		},
	}
}

// Run executes one load run and returns its measurements. A non-nil error
// is reserved for configuration mistakes; request failures are data
// (Result.Failed, Result.Errors).
func Run(ctx context.Context, spec *Spec) (*Result, error) {
	if len(spec.Routes) == 0 {
		return nil, errors.New("bench: no route configured (Spec.Routes is empty)")
	}
	if spec.Streams <= 0 {
		return nil, fmt.Errorf("bench: streams must be > 0, got %d", spec.Streams)
	}
	if spec.Requests <= 0 && spec.Duration <= 0 {
		return nil, errors.New("bench: set Spec.Requests or Spec.Duration so the run ends")
	}
	mode := spec.Mode
	if mode == "" {
		mode = ModeStream
	}
	if mode != ModeStream && mode != ModeSend {
		return nil, fmt.Errorf("bench: unknown mode %q (want stream|send)", mode)
	}
	client := spec.HTTPClient
	if client == nil {
		client = tunedClient(spec.Streams, spec.DisableKeepAlives)
	}

	res := &Result{
		Label:         spec.Label,
		Mode:          string(mode),
		Message:       spec.Message,
		Routes:        len(spec.Routes),
		Streams:       spec.Streams,
		RequestBudget: spec.Requests,
		Started:       time.Now().UTC(),
		Errors:        map[string]int{},
	}
	obs := &GatewayObservation{}
	if spec.AdminURL != "" {
		if snap, err := scrapeAdmin(ctx, spec.AdminURL); err == nil {
			obs.Before = snap
		} else {
			obs.ScrapeFailed = "before: " + err.Error()
		}
		obs.Versions = fetchVersions(ctx, spec.AdminURL)
	}
	res.Gateway = obs

	// Counters (atomics); the error-kind map lives under a small mutex.
	var completed, failed, retried, attempted atomic.Int64
	var events, bytesSent, bytesRecv atomic.Int64
	var errMu sync.Mutex
	recordErr := func(kind string) {
		errMu.Lock()
		res.Errors[kind]++
		errMu.Unlock()
	}

	// Latency samples: successes only, in milliseconds.
	var latMu sync.Mutex
	lat := make([]int64, 0, spec.Streams)
	sample := func(ms int64) {
		latMu.Lock()
		lat = append(lat, ms)
		latMu.Unlock()
	}

	// Request budget: each new request atomically claims one slot; a claim
	// landing below zero means the tank is empty.
	remaining := int64(spec.Requests)
	claim := func() bool {
		if spec.Requests <= 0 {
			return true // only the clock (Duration) can stop the run
		}
		return atomic.AddInt64(&remaining, -1) >= 0
	}

	runCtx := ctx
	if spec.Duration > 0 {
		var cl context.CancelFunc
		runCtx, cl = context.WithTimeout(ctx, spec.Duration)
		defer cl()
	}
	start := time.Now()

	// Resource observation: the active-streams gauge is instantaneous, so
	// poll while the run lasts and keep the maximum.
	if spec.AdminURL != "" {
		peakCtx, stop := context.WithCancel(ctx)
		defer stop()
		go func() {
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-peakCtx.Done():
					return
				case <-t.C:
					if s, err := scrapeAdmin(peakCtx, spec.AdminURL); err == nil &&
						s.ActiveStreams > obs.ActiveStreamsPeak {
						obs.ActiveStreamsPeak = s.ActiveStreams
					}
				}
			}
		}()
	}

	// Progress line once per second.
	if spec.Progress != nil {
		pw := spec.Progress
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					done, fail, attn := completed.Load(), failed.Load(), attempted.Load()
					inflight := attn - done - fail
					if inflight < 0 {
						inflight = 0
					}
					bw := time.Since(start).Seconds()
					rate := float64(done) / math.Max(bw, 1e-9)
					fmt.Fprintf(pw, "[%6.1fs] %-16s done=%6d failed=%4d inflight=%6d rate=%8.1f rps\n",
						bw, spec.Label, done, fail, inflight, rate)
				}
			}
		}()
	}

	// One goroutine per worker stream; Ramp staggers the starts so 10k
	// workers do not hit the listener in one SYN burst.
	var wg sync.WaitGroup
	for i := 0; i < spec.Streams; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if spec.Ramp > 0 && spec.Streams > 1 {
				delay := time.Duration(int64(spec.Ramp) * int64(id) / int64(spec.Streams))
				t := time.NewTimer(delay)
				select {
				case <-runCtx.Done():
					t.Stop()
					return
				case <-t.C:
				}
			}
			route := spec.Routes[id%len(spec.Routes)]
			timeout := spec.Timeout
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			for {
				if runCtx.Err() != nil {
					return
				}
				if !claim() {
					return
				}
				attempted.Add(1)
				if doRequest(runCtx, client, route, mode, spec.Message, timeout,
					spec.Retries, id, sample, &retried, &bytesSent, &bytesRecv, &events, recordErr) {
					completed.Add(1)
				} else if runCtx.Err() != nil {
					// The run budget (--duration) or a signal cut this
					// exchange short. It is not a request failure: counting
					// the workers still in flight at cutoff as failures would
					// turn every healthy duration-bounded tier red.
				} else {
					failed.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()

	res.WallSeconds = time.Since(start).Seconds()
	res.Completed = int(completed.Load())
	res.Failed = int(failed.Load())
	res.Retried = int(retried.Load())
	res.Attempts = int(attempted.Load())
	if res.Completed > 0 {
		res.EventsPerStream = float64(events.Load()) / float64(res.Completed)
	}
	res.BytesSent = bytesSent.Load()
	res.BytesReceived = bytesRecv.Load()
	if res.WallSeconds > 0 {
		res.ThroughputRPS = float64(res.Completed) / res.WallSeconds
	}
	if len(res.Errors) == 0 {
		res.Errors = nil // keep the published JSON tidy
	}

	// Latency percentiles: nearest-rank over the sorted success samples.
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	res.Latency = pctSummary(lat)

	if spec.AdminURL != "" {
		if snap, err := scrapeAdmin(ctx, spec.AdminURL); err == nil {
			obs.After = snap
		} else if obs.ScrapeFailed == "" {
			obs.ScrapeFailed = "after: " + err.Error()
		}
	}
	return res, nil
}

// pctSummary computes the published percentile block (nearest-rank).
func pctSummary(sorted []int64) Latencies {
	if len(sorted) == 0 {
		return Latencies{}
	}
	var sum int64
	for _, v := range sorted {
		sum += v
	}
	at := func(q float64) int64 {
		idx := int(math.Ceil(q*float64(len(sorted)))) - 1
		return sorted[min(max(idx, 0), len(sorted)-1)]
	}
	return Latencies{
		P50MS:  float64(at(0.50)),
		P95MS:  float64(at(0.95)),
		P99MS:  float64(at(0.99)),
		P999MS: float64(at(0.999)),
		MeanMS: float64(sum) / float64(len(sorted)),
		MaxMS:  float64(sorted[len(sorted)-1]),
	}
}

// doRequest drives one request to its terminal state, retrying transient
// failures inside the request's own deadline. Every failed attempt is
// recorded by kind, so Errors explains both Retried and Failed.
func doRequest(ctx context.Context, client *http.Client, route string, mode Mode,
	text string, timeout time.Duration, retries, worker int,
	sample func(ms int64),
	retried, bytesSent, bytesRecv, events *atomic.Int64,
	recordErr func(kind string)) bool {
	deadline := time.Now().Add(timeout)
	for attempt := 0; ; attempt++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			recordErr(ErrTimeout)
			return false
		}
		areq, cancel := context.WithDeadline(ctx, deadline)
		out := requestOnce(areq, client, route, mode, text, worker, bytesSent, bytesRecv, events)
		cancel()
		if out.ok {
			sample(out.ms)
			return true
		}
		if ctx.Err() != nil {
			// The caller's budget ended mid-flight (duration deadline or
			// interrupt): the attempt never got its verdict, so it is not
			// recorded as a request failure.
			return false
		}
		recordErr(out.kind)
		if attempt >= retries || !retryable(out.kind) {
			return false
		}
		// Backoff: 50ms doubling, jittered, capped at 500ms and by the
		// remaining deadline; one shared deadline prevents pathological
		// retry loops from stretching the run unbounded.
		d := backoffDelay(attempt)
		if d > remaining {
			d = remaining
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
		retried.Add(1)
	}
}

// retryable classifies transport-level and transient 5xx failures as worth
// one more try (Windows port pressure and stream-cap 503s surface here).
// Protocol errors and timeouts are reported verbatim, never retried.
func retryable(kind string) bool {
	switch kind {
	case ErrConnect, ErrTransport:
		return true
	case ErrHTTP + "500", ErrHTTP + "502", ErrHTTP + "503", ErrHTTP + "504":
		return true
	}
	return false
}

func backoffDelay(attempt int) time.Duration {
	base := 50 << attempt // 50ms, 100ms, 200ms...
	if base > 400 {
		base = 400
	}
	return time.Duration(rand.Int64N(int64(base + 50)))
}

// Wire shapes the driver reads on the A2A v1.0 wire (the a2a-go v2 event
// union, see a2a.StreamResponse).
type wireStreamEvent struct {
	Error  *wireError       `json:"error,omitempty"`
	Result *wireEventResult `json:"result,omitempty"`
}

type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type wireEventResult struct {
	Message        *wireMessage        `json:"message,omitempty"`
	Task           *wireTask           `json:"task,omitempty"`
	StatusUpdate   *wireStatusUpdate   `json:"statusUpdate,omitempty"`
	ArtifactUpdate *wireArtifactUpdate `json:"artifactUpdate,omitempty"`
}

type wireMessage struct {
	ID   string `json:"messageId"`
	Role string `json:"role"`
}

type wireTask struct {
	ID     string `json:"id"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
}

type wireStatusUpdate struct {
	TaskID string `json:"taskId"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
}

type wireArtifactUpdate struct {
	TaskID string `json:"taskId"`
}

// countingReader counts response bytes as consumers read them, keeping SSE
// byte totals network-accurate.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// requestOutcome is the raw per-attempt verdict before aggregation.
type requestOutcome struct {
	ok   bool
	kind string // error kind ("" on success)
	ms   int64  // end-to-end latency on success
}

// envelope builds the raw JSON-RPC request body: fresh messageId per
// request (required by A2A), ROLE_USER, one flattened text part, and a
// unique JSON-RPC id per attempt for cross-checking counters downstream.
func envelope(method, text string, id int) []byte {
	uid := time.Now().UnixNano()
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      fmt.Sprintf("loadgen-%d-%d", id, uid),
		"params": map[string]any{
			"message": map[string]any{
				"messageId": fmt.Sprintf("msg-%d-%d", id, uid),
				"role":      "ROLE_USER",
				"parts":     []map[string]any{{"text": text}},
			},
		},
	}
	raw, _ := json.Marshal(req)
	return raw
}

// requestOnce performs exactly one attempt: a send round trip or an SSE
// stream driven to its terminal state. It never retries.
func requestOnce(ctx context.Context, client *http.Client, route string, mode Mode,
	text string, worker int, bytesSent, bytesRecv, events *atomic.Int64) requestOutcome {
	method := methodSend
	if mode == ModeStream {
		method = methodStream
	}
	body := envelope(method, text, worker)
	bytesSent.Add(int64(len(body)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, route, bytes.NewReader(body))
	if err != nil {
		return requestOutcome{kind: ErrBody}
	}
	req.Header.Set("Content-Type", "application/json")
	if mode == ModeStream {
		req.Header.Set("Accept", sseAccept)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return requestOutcome{kind: ErrTimeout}
		}
		if ctx.Err() == context.Canceled {
			return requestOutcome{kind: ErrCanceled}
		}
		if isConnReset(err) {
			return requestOutcome{kind: ErrTransport}
		}
		return requestOutcome{kind: ErrConnect}
	}
	defer func() { _ = resp.Body.Close() }()
	cr := &countingReader{r: resp.Body, n: bytesRecv}

	if resp.StatusCode != http.StatusOK {
		// Drain bounded: gateway/agent error bodies are small documents.
		_, _ = io.Copy(io.Discard, io.LimitReader(cr, 4<<20))
		return requestOutcome{kind: ErrHTTP + strconv.Itoa(resp.StatusCode)}
	}

	if mode == ModeStream {
		ok, kind, ev := readSSE(cr)
		if !ok {
			return requestOutcome{kind: kind}
		}
		events.Add(int64(ev))
		return requestOutcome{ok: true, ms: sinceMs(start)}
	}

	// send mode: one JSON-RPC response carries the terminal state.
	raw, err := io.ReadAll(io.LimitReader(cr, 4<<20))
	if err != nil {
		return requestOutcome{kind: ErrTransport}
	}
	out := parseSendResult(raw)
	if out.ok {
		out.ms = sinceMs(start)
	}
	return out
}

// readSSE scans WHATWG SSE framing (events end at a blank line; data
// fields of one event join) until a data block carries a terminal state
// or an error object. Returns (success, error-kind, data-block count).
func readSSE(src io.Reader) (bool, string, int) {
	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var event bytes.Buffer
	count := 0
	classify := func(payload []byte) (ok bool, kind string, stop bool) {
		var we wireStreamEvent
		if err := json.Unmarshal(payload, &we); err != nil {
			return false, ErrBody, true
		}
		if we.Error != nil {
			return false, ErrA2A, true
		}
		r := we.Result
		if r == nil {
			return true, "", false
		}
		switch {
		case r.Message != nil: // a bare agent message terminates processing
			return true, "", true
		case r.StatusUpdate != nil && terminalTaskStates[r.StatusUpdate.Status.State]:
			return true, "", true
		case r.Task != nil && r.Task.ID != "" && terminalTaskStates[r.Task.Status.State]:
			// A task echo already in a terminal state (resubscribe paths).
			return true, "", true
		}
		return true, "", false // task echo / working status / artifact: continue
	}
	flush := func() (ok, stop bool, kind string) {
		if event.Len() == 0 {
			return true, false, ""
		}
		ok, kind, stop = classify(event.Bytes())
		event.Reset()
		return ok, stop, kind
	}
	for sc.Scan() {
		line := sc.Bytes()
		if bytes.HasPrefix(line, []byte("data:")) {
			payload := bytes.TrimPrefix(line, []byte("data:"))
			payload = bytes.TrimPrefix(payload, []byte(" "))
			if event.Len() > 0 {
				event.WriteByte('\n') // spec: data lines of one event join
			}
			event.Write(payload)
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 { // blank line ends the event
			count++
			ok, stop, kind := flush()
			if !ok {
				return false, kind, count
			}
			if stop {
				return true, "", count
			}
		}
		// Other fields (id:, comments such as ": keep-alive") are noise
		// for terminal detection and do not end events.
	}
	// EOF: a trailing event without a terminating blank line still counts
	// as one event (the SDK always writes the blank line, but never
	// penalize a well-formed-but-close-shy stream).
	ok, stop, kind := flush()
	if ok && stop {
		return true, "", count + 1
	}
	if !ok {
		return false, kind, count + 1
	}
	return false, ErrSSEEOF, count
}

// isConnReset distinguishes transport breakage (worth retrying) from dial
// failures by their message shapes.
func isConnReset(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "EOF")
}

func sinceMs(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

// parseSendResult validates a non-streaming JSON-RPC result: the event
// union carrying a Task or Message is the terminal state (A2A v1.0
// SendMessage contract; the wrapper is a2a-go's StreamResponse).
func parseSendResult(raw []byte) requestOutcome {
	var we wireStreamEvent
	if err := json.Unmarshal(raw, &we); err != nil {
		return requestOutcome{kind: ErrBody}
	}
	if we.Error != nil {
		return requestOutcome{kind: ErrA2A}
	}
	if r := we.Result; r != nil && (r.Message != nil || r.Task != nil) {
		return requestOutcome{ok: true}
	}
	return requestOutcome{kind: ErrBody}
}
