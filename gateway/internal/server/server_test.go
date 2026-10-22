// Graceful shutdown tests (GW-7): real listeners, real proxying, real
// drain. Each test cancels the Start context like the signal handler does
// (signal → cancel → drain-with-deadline → hard close escalation) and
// asserts the externally visible contract: health probes flip, in-flight
// requests finish inside the grace window, escalation stays bounded, and
// telemetry flushes on every exit path.
package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/storage"
)

const (
	testAgentName     = "up"
	testDrainGrace    = 400 * time.Millisecond
	testHardGrace     = 300 * time.Millisecond
	streamChunkCount  = 10
	streamChunkDelay  = 5 * time.Millisecond
	testStartupLimit  = 5 * time.Second
	testExitLimit     = 5 * time.Second
	testHardCloseBulb = 2 * time.Second // hard upper bound: shutdown must never hang
)

// ---------------------------------------------------------------- logging

type capturedRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// captureLog records every log record the gateway emits, so the tests can
// watch for stray errors/leak warnings and read the bound :0 listener
// addresses from the startup record.
type captureLog struct {
	mu      sync.Mutex
	records []capturedRecord
}

func newCaptureLog() *captureLog { return &captureLog{} }

func (c *captureLog) logger() *slog.Logger { return slog.New(&captureHandler{c: c}) }

type captureHandler struct{ c *captureLog }

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	rec := capturedRecord{level: r.Level, msg: r.Message, attrs: map[string]string{}}
	r.Attrs(func(a slog.Attr) bool {
		if v, ok := a.Value.Resolve().Any().(string); ok {
			rec.attrs[a.Key] = v
		}
		return true
	})
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	h.c.records = append(h.c.records, rec)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (c *captureLog) find(msg string) (capturedRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.records) - 1; i >= 0; i-- {
		if c.records[i].msg == msg {
			return c.records[i], true
		}
	}
	return capturedRecord{}, false
}

func (c *captureLog) hasLevel(level slog.Level) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.level >= level {
			return true
		}
	}
	return false
}

func (c *captureLog) levelMessages(level slog.Level) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.records {
		if r.level >= level {
			out = append(out, r.msg)
		}
	}
	return out
}

func (c *captureLog) hasMessage(msg string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.msg == msg {
			return true
		}
	}
	return false
}

// snapshot marks the current record count; later assertions can scope to
// records emitted after the mark (log noise from construction time is
// separate from shutdown behaviour).
func (c *captureLog) snapshot() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

func (c *captureLog) hasLevelAfter(mark int, level slog.Level) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records[mark:] {
		if r.level >= level {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------ helpers

// pollFor retries fn until it holds true; a dead condition is a Fatal.
func pollFor(t *testing.T, timeout, interval time.Duration, fn func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("condition not met within %v: %s", timeout, desc)
}

// adminGet probes one admin path; -1 means the dial itself failed (which
// is expected once Shutdown has closed the listeners and also counts as
// poll-worthwhile evidence for orchestrators, so it retries via pollFor).
func adminGet(client *http.Client, addr, path string) int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return -1
	}
	resp, err := client.Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// keepAliveClient probes over one reusable connection. Shutdown closes
// the listeners immediately, so probes dialled after it starts are
// refused; established connections observe the 503 flip (GW-7 semantics
// as seen by an orchestrator with an open check).
func keepAliveClient() *http.Client {
	return &http.Client{
		Timeout: time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 1,
			IdleConnTimeout:     30 * time.Second,
		},
	}
}

// shutdownTestConfig builds a validated gateway config with :0 listeners,
// the management API off, the given upstream, and the given drain grace.
// telemetryYAML is the `telemetry:` block body; empty keeps export
// disabled (W3C propagation only, per the M2 contract).
func shutdownTestConfig(t *testing.T, upstreamURL string, grace time.Duration, telemetryYAML string) *config.Config {
	t.Helper()
	doc := fmt.Sprintf(`
listeners:
  a2a:
    addr: "127.0.0.1:0"
  admin:
    addr: "127.0.0.1:0"
telemetry:
%s
limits:
  shutdown_grace: %q
  max_concurrent_streams: 32
agents:
  - name: %s
    upstreams:
      - url: %q
`, telemetryYAML, grace.String(), testAgentName, upstreamURL)
	cfg, err := config.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatalf("load shutdown test config: %v", err)
	}
	// The `enabled: false` YAML leaf is clobbered by the bool default
	// (walk treats false as unset), so disable the management API the same
	// way the integration harness rewrites config after loading.
	cfg.Management.Enabled = false
	return cfg
}

// telemetryYAMLBlock enables the real OTLP http/protobuf exporter against
// an in-process collector with a fast batch flush for the flush tests.
func telemetryYAMLBlock(endpoint string) string {
	return fmt.Sprintf(`  otlp_endpoint: %q
  otlp_protocol: http/protobuf
  batch_size: 128
  batch_timeout: 50ms
  queue_size: 256
`, endpoint)
}

// -------------------------------------------------------- fake upstream

// fakeUpstream is the one controllable upstream the shutdown tests proxy
// through real gateway listeners: it can answer statically, stream an
// SSE-ish body slowly, or hold a proxied request until released.
type fakeUpstream struct {
	srv         *httptest.Server
	mode        atomic.Value // string
	enteredOnce sync.Once
	entered     chan struct{}
	releaseOnce sync.Once
	releaseCh   chan struct{}
	cardGets    atomic.Int64
	proxied     atomic.Int64
}

const upstreamCardTemplate = `{"name":"up","version":"1.0","capabilities":{"streaming":true},"supportedInterfaces":[{"url":%q,"protocolBinding":"JSONRPC"}]}`

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	u := &fakeUpstream{entered: make(chan struct{}), releaseCh: make(chan struct{})}
	u.mode.Store("block")
	u.srv = httptest.NewServer(u)
	t.Cleanup(u.srv.Close)
	return u
}

func (u *fakeUpstream) URL() string      { return u.srv.URL }
func (u *fakeUpstream) setMode(m string) { u.mode.Store(m) }
func (u *fakeUpstream) release()         { u.releaseOnce.Do(func() { close(u.releaseCh) }) }

func (u *fakeUpstream) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-u.entered:
	case <-time.After(testStartupLimit):
		t.Fatal("upstream never received a proxied request")
	}
}

func (u *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, registry.WellKnownAgentCardPath) {
		// The card refresher warms on startup in every mode; answer it
		// immediately so background fetches never block on the test.
		u.cardGets.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, upstreamCardTemplate, u.srv.URL)
		return
	}
	u.proxied.Add(1)
	switch u.mode.Load().(string) {
	case "static":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	case "stream":
		u.enteredOnce.Do(func() { close(u.entered) })
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for i := 0; i < streamChunkCount; i++ {
			fmt.Fprintf(w, "data: chunk-%02d\n\n", i)
			if f != nil {
				f.Flush()
			}
			time.Sleep(streamChunkDelay)
		}
		_, _ = io.WriteString(w, "event: done\ndata: [DONE]\n\n")
	default: // "block": hold the request until released or disconnected
		u.enteredOnce.Do(func() { close(u.entered) })
		select {
		case <-r.Context().Done(): // gateway hard-closed its connections
		case <-u.releaseCh:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"released":true}`)
		case <-time.After(30 * time.Second): // absolute bound: never wedge a test
		}
	}
}

// streamingBody returns the exact SSE body the stream mode emits, used by
// the client-completeness assertions.
func streamingBody() string {
	var b strings.Builder
	for i := 0; i < streamChunkCount; i++ {
		fmt.Fprintf(&b, "data: chunk-%02d\n\n", i)
	}
	b.WriteString("event: done\ndata: [DONE]\n\n")
	return b.String()
}

// ------------------------------------------------- gateway test fixture

type clientResult struct {
	resp *http.Response
	body []byte
	err  error
}

func doRequest(url string, results chan<- clientResult) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		results <- clientResult{err: err}
		return
	}
	body, rerr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	results <- clientResult{resp: resp, body: body, err: rerr}
}

// runningGateway drives one real server.New + Start round and stays
// available to the test for in-package access to meters, telemetry and
// the span store.
type runningGateway struct {
	server   *Server
	logs     *captureLog
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	startErr error
}

// startGateway builds the server and runs Start exactly as the signal
// path does (cancel the context to trigger the drain).
func startGateway(t *testing.T, cfg *config.Config, logs *captureLog) *runningGateway {
	t.Helper()
	s, err := New(cfg, logs.logger())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	g := &runningGateway{server: s, logs: logs, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	go func() {
		err := s.Start(ctx)
		g.mu.Lock()
		g.startErr = err
		g.mu.Unlock()
		close(g.done)
	}()
	// Safety net for every test: a shutdown path that hangs must fail the
	// test on cleanup instead of leaking goroutines into the next test.
	t.Cleanup(func() {
		g.cancel()
		select {
		case <-g.done:
		case <-time.After(15 * time.Second):
			t.Error("Start did not return within 15s of the cleanup cancel (GW-7 hang)")
		}
	})
	return g
}

// waitStarted polls the captured startup record for the bound :0 listener
// addresses.
func (g *runningGateway) waitStarted(t *testing.T) (a2aAddr, adminAddr string) {
	t.Helper()
	deadline := time.Now().Add(testStartupLimit)
	for time.Now().Before(deadline) {
		if rec, ok := g.logs.find("kyurem gateway starting"); ok {
			a2aAddr, adminAddr = rec.attrs["a2a_addr"], rec.attrs["admin_addr"]
			if a2aAddr != "" && adminAddr != "" {
				return a2aAddr, adminAddr
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("gateway did not report startup within 5s")
	return "", ""
}

// awaitExit waits for Start to return (bounded) and yields its error.
func (g *runningGateway) awaitExit(t *testing.T, maxWait time.Duration) error {
	t.Helper()
	select {
	case <-g.done:
	case <-time.After(maxWait):
		t.Fatalf("Start did not return within %v (shutdown must never hang)", maxWait)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.startErr
}

// -------------------------------------------------------------- test a

// GW-7 drain-in-progress semantics: the moment shutdown begins both
// health probes answer 503 while the in-flight request is still allowed
// to complete; Start then returns nil without noisy logs.
func TestShutdownHealthFlipWhileInFlightDrains(t *testing.T) {
	up := newFakeUpstream(t)
	defer up.release()
	logs := newCaptureLog()
	g := startGateway(t, shutdownTestConfig(t, up.URL(), testDrainGrace, ""), logs)
	a2aAddr, adminAddr := g.waitStarted(t)

	pollFor(t, testStartupLimit, 10*time.Millisecond, func() bool {
		return adminGet(keepAliveClient(), adminAddr, "/healthz") == http.StatusOK &&
			adminGet(keepAliveClient(), adminAddr, "/readyz") == http.StatusOK
	}, "healthz and readyz to come up")

	// Observation mirror: the *same* server instance's production admin
	// mux on a second listener. The primary listener closes the moment
	// Shutdown starts (fresh dials are refused, which is exactly what
	// orchestrators see), while this listener keeps serving the real
	// handler so the 503 flip itself stays observable (GW-7).
	mirror := httptest.NewServer(g.server.adminHandler())
	t.Cleanup(mirror.Close)
	mirrorAddr := strings.TrimPrefix(mirror.URL, "http://")

	results := make(chan clientResult, 1)
	go doRequest("http://"+a2aAddr+"/a2a/"+testAgentName+"/drain", results)
	up.awaitEntered(t)

	mark := logs.snapshot()
	g.cancel()

	// The flip must happen once shutdown starts; probe with sticky flags
	// because the two endpoints may be observed on separate passes.
	var sawHealth, sawReady bool
	pollFor(t, 2*time.Second, 10*time.Millisecond, func() bool {
		if adminGet(keepAliveClient(), mirrorAddr, "/healthz") == http.StatusServiceUnavailable {
			sawHealth = true
		}
		if adminGet(keepAliveClient(), mirrorAddr, "/readyz") == http.StatusServiceUnavailable {
			sawReady = true
		}
		return sawHealth && sawReady
	}, "healthz and readyz to flip to 503 once shutdown starts (GW-7)")

	// Orchestrator view of the real listener: once draining, no further
	// successful probe is possible (503 in the pre-close window, then
	// refusal).
	if code := adminGet(keepAliveClient(), adminAddr, "/healthz"); code == http.StatusOK {
		t.Errorf("healthz still answered 200 after shutdown began (status %d)", code)
	}
	if code := adminGet(keepAliveClient(), adminAddr, "/readyz"); code == http.StatusOK {
		t.Errorf("readyz still answered 200 after shutdown began (status %d)", code)
	}

	// Still draining, the in-flight request is allowed to finish.
	up.release()
	res := <-results
	if res.err != nil {
		t.Fatalf("in-flight request failed during drain: %v", res.err)
	}
	if res.resp.StatusCode != http.StatusOK {
		t.Errorf("in-flight request status = %d, want 200", res.resp.StatusCode)
	}
	if string(res.body) != `{"released":true}` {
		t.Errorf("in-flight response body = %q, want the upstream body delivered in full", res.body)
	}

	if err := g.awaitExit(t, testExitLimit); err != nil {
		t.Fatalf("graceful shutdown returned error: %v", err)
	}
	if logs.hasLevelAfter(mark, slog.LevelError) {
		t.Errorf("clean drain logged errors: %v", logs.levelMessages(slog.LevelError))
	}
}

// -------------------------------------------------------------- test b

// GW-7: a streaming request that fits inside the grace window completes
// end-to-end during the drain: the client gets the whole SSE body, Start
// returns promptly, and nothing warns about leaks afterwards.
func TestShutdownDrainsStreamingResponseFully(t *testing.T) {
	up := newFakeUpstream(t)
	up.setMode("stream")
	logs := newCaptureLog()
	g := startGateway(t, shutdownTestConfig(t, up.URL(), testDrainGrace, ""), logs)
	a2aAddr, _ := g.waitStarted(t)

	results := make(chan clientResult, 1)
	go doRequest("http://"+a2aAddr+"/a2a/"+testAgentName+"/stream", results)
	up.awaitEntered(t)

	mark := logs.snapshot()
	g.cancel()
	cancelAt := time.Now()
	res := <-results
	if res.err != nil {
		t.Fatalf("streaming request failed during drain: %v", res.err)
	}
	if res.resp.StatusCode != http.StatusOK {
		t.Errorf("streaming request status = %d, want 200", res.resp.StatusCode)
	}
	if want := streamingBody(); string(res.body) != want {
		t.Errorf("streaming body incomplete:\n got %q\nwant %q", res.body, want)
	}

	if err := g.awaitExit(t, testExitLimit); err != nil {
		t.Fatalf("graceful shutdown returned error: %v", err)
	}
	if elapsed := time.Since(cancelAt); elapsed > testHardCloseBulb {
		t.Errorf("Start consumed %v though the stream finished well within the %v grace",
			elapsed, testDrainGrace)
	}

	if logs.hasLevelAfter(mark, slog.LevelWarn) {
		t.Errorf("no leak warnings expected after a clean drain, got %v",
			logs.levelMessages(slog.LevelWarn))
	}
	if streams := testutil.ToFloat64(g.server.mtr.ActiveStreams.WithLabelValues(testAgentName)); streams != 0 {
		t.Errorf("active streams gauge = %v after the drain; stream accounting leaked", streams)
	}
}

// -------------------------------------------------------------- test c

// GW-7 hard close escalation: the upstream blocks past
// limits.shutdown_grace, Start must return promptly (bounded by grace +
// small epsilon), the client sees the connection die, and the escalation
// warning must appear. The process must never hang.
func TestShutdownHardClosesWhenGraceExpires(t *testing.T) {
	up := newFakeUpstream(t)
	logs := newCaptureLog()
	g := startGateway(t, shutdownTestConfig(t, up.URL(), testHardGrace, ""), logs)
	a2aAddr, _ := g.waitStarted(t)

	results := make(chan clientResult, 1)
	go doRequest("http://"+a2aAddr+"/a2a/"+testAgentName+"/block-forever", results)
	up.awaitEntered(t)

	g.cancel()
	cancelAt := time.Now()
	if err := g.awaitExit(t, testExitLimit); err != nil {
		t.Fatalf("hard-close shutdown returned error: %v", err)
	}
	elapsed := time.Since(cancelAt)

	if elapsed < testHardGrace-75*time.Millisecond {
		t.Errorf("Start returned after %v, before the %v grace had elapsed", elapsed, testHardGrace)
	}
	if elapsed > testHardCloseBulb {
		t.Errorf("hard close took %v; graceful shutdown must be bounded by grace + epsilon", elapsed)
	}

	res := <-results
	if res.err == nil {
		t.Errorf("client completed fully despite hard close: %q", res.body)
	}

	if !logs.hasMessage("graceful shutdown deadline exceeded; forcing close") {
		t.Errorf("expected the hard-close escalation warning; warnings: %v", logs.levelMessages(slog.LevelWarn))
	}
	up.release()
}

// -------------------------------------------------------------- test d

// Flush guarantee: spans ended before (and, for the in-flight one, during)
// the drain are all exported after Start returns: ended == exported and
// zero drops (TR-5), and every span the sink published stays queryable in
// the memory store.
func TestShutdownFlushesTelemetry(t *testing.T) {
	up := newFakeUpstream(t)
	collector := newFakeCollector(t)
	logs := newCaptureLog()

	cfg := shutdownTestConfig(t, up.URL(), testDrainGrace, telemetryYAMLBlock(collector.srv.URL))
	g := startGateway(t, cfg, logs)
	a2aAddr, _ := g.waitStarted(t)

	// Four quick requests end their spans before shutdown.
	up.setMode("static")
	quickies := make(chan clientResult, 4)
	for i := 0; i < 4; i++ {
		go doRequest("http://"+a2aAddr+"/a2a/"+testAgentName+"/quick", quickies)
	}
	for i := 0; i < 4; i++ {
		if q := <-quickies; q.err != nil {
			t.Fatalf("static proxied request failed: %v", q.err)
		}
	}
	mtr := g.server.mtr
	pollFor(t, 3*time.Second, 10*time.Millisecond, func() bool {
		ended, exported, _, _, dropped := mtr.TelemetryCounters()
		return ended == 4 && exported == 4 && len(dropped) == 0
	}, "the four spans to be exported with zero drops")

	// Fifth request blocks; its span will end **during** the drain.
	up.setMode("block")
	results := make(chan clientResult, 1)
	go doRequest("http://"+a2aAddr+"/a2a/"+testAgentName+"/flushhang", results)
	up.awaitEntered(t)

	g.cancel()
	// Released immediately: the response (and its span end) lands inside
	// the drain window, so even shutdown-time span ends must be flushed.
	up.release()
	if err := g.awaitExit(t, testExitLimit); err != nil {
		t.Fatalf("graceful shutdown returned error: %v", err)
	}

	ended, exported, failures, retries, dropped := mtr.TelemetryCounters()
	if ended != 5 || exported != 5 {
		t.Errorf("flush guarantee violated: want ended=5 exported=5, got ended=%d exported=%d", ended, exported)
	}
	if failures != 0 || retries != 0 || len(dropped) != 0 {
		t.Errorf("flush guarantee violated: failures=%d retries=%d dropped=%v", failures, retries, dropped)
	}
	if depth, _ := mtr.TelemetryQueue(); depth != 0 {
		t.Errorf("export queue depth = %d after shutdown, want 0", depth)
	}
	store := g.server.store.(*storage.MemoryStore)
	pollFor(t, 3*time.Second, 10*time.Millisecond, func() bool {
		return store.Len() == 5
	}, "all five spans (including the one ended during the drain) to remain in the memory store")
	if collector.hits.Load() == 0 {
		t.Error("the collector received no export batches")
	}
}

// ------------------------------------------------------------ collector

// fakeCollector is a minimal OTLP/HTTP collector: it counts export
// batches and answers 200 so the real exporter pipeline (queue, batching,
// retry accounting) runs untouched.
type fakeCollector struct {
	srv  *httptest.Server
	hits atomic.Int64
}

func newFakeCollector(t *testing.T) *fakeCollector {
	t.Helper()
	c := &fakeCollector{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		c.hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// -------------------------------------------------------------- test e

// Bind failure exit path: Start returns the bind error promptly and the
// deferred telemetry flush still runs once (no panic, no lost span).
func TestStartBindFailureFlushesTelemetry(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer occupied.Close()

	collector := newFakeCollector(t)
	cfg := shutdownTestConfig(t, "http://127.0.0.1:1", testDrainGrace, telemetryYAMLBlock(collector.srv.URL))
	cfg.Listeners.Admin.Addr = occupied.Addr().String()

	s, err := New(cfg, newCaptureLog().logger())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	// A span ended before Start must survive the failed bind: the flush
	// runs on every exit path.
	_, span := s.tel.StartSpan(context.Background(), "preload span")
	span.End()

	start := time.Now()
	startErr := s.Start(context.Background())
	elapsed := time.Since(start)

	if startErr == nil {
		t.Fatal("Start must return the bind error on an occupied port")
	}
	if !strings.Contains(startErr.Error(), "listen tcp") {
		t.Errorf("bind error does not look like a listen failure: %v", startErr)
	}
	if elapsed > 5*time.Second {
		t.Errorf("bind failure path took %v; exit must be quick", elapsed)
	}

	mtr := s.mtr
	pollFor(t, 3*time.Second, 10*time.Millisecond, func() bool {
		ended, exported, _, _, dropped := mtr.TelemetryCounters()
		return ended == 1 && exported == 1 && len(dropped) == 0
	}, "the preload span to be flushed exactly once on the failed bind")
	if collector.hits.Load() == 0 {
		t.Error("no export attempt reached the collector on the bind-failure path")
	}
}

// ------------------------------------------------- card refresher leak

// refresherTestConfig is shutdownTestConfig with a short card cache TTL
// so the refresher cadence (ttl/2 .. ttl/2+ttl/4) is observable in
// seconds.
func refresherTestConfig(t *testing.T, upstreamURL string, grace time.Duration) *config.Config {
	t.Helper()
	cfg := shutdownTestConfig(t, upstreamURL, grace, "")
	cfg.CardCache.TTL = config.Duration(time.Second)
	return cfg
}

// GW-7: the card refresher must stop with the run. Leaked refresh loops
// keep fetching upstream cards forever after Start returns. With a 1s
// card TTL the refresh delay is 0.5-0.75s, so leaked loops show up as new
// card fetches long after shutdown.
func TestShutdownStopsCardRefresher(t *testing.T) {
	up := newFakeUpstream(t)
	up.setMode("static")
	logs := newCaptureLog()
	g := startGateway(t, refresherTestConfig(t, up.URL(), testDrainGrace), logs)
	g.waitStarted(t)

	pollFor(t, testStartupLimit, 10*time.Millisecond, func() bool {
		return up.cardGets.Load() >= 1
	}, "the refresher to warm the agent card")

	g.cancel()
	if err := g.awaitExit(t, testExitLimit); err != nil {
		t.Fatalf("graceful shutdown returned error: %v", err)
	}

	before := up.cardGets.Load()
	time.Sleep(1500 * time.Millisecond) // > leaked-loop refresh interval (0.5-0.75s)
	if after := up.cardGets.Load(); after != before {
		t.Errorf("card fetches ran after shutdown: %d -> %d; refresher leaked", before, after)
	}
}
