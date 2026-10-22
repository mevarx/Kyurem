// Test-only stack for the bench package: an in-process gateway (the real
// proxy handler, registry, card cache and telemetry, wired like server.New
// does) behind an A2A agent that streams the go-echo task lifecycle over
// SSE. The agent is hand-rolled on the A2A v1.0 wire so the load tiers stay
// self-contained: the integration harness lives in another package and the
// tier runs must not import it.
//
// Why not spawn the real gateway + go-echo processes here? The published
// reproducibility path is the demo scripts (demo/scenarios/bench-load.*),
// which do exactly that with the CLI load generator. The Go tests instead
// keep everything in-process: no build step, port-safe under `go test
// ./...`, race-detector friendly for the 100-stream smoke tier, and the
// same wire shape (identical envelopes, SSE framing and terminal rules).
package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/proxy"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/storage"
	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

// cardPath is the A2A discovery location served by the agent (and rewritten
// by the gateway when clients fetch it).
const cardPath = "/.well-known/agent-card.json"

// errMsgPrefix is the echoed reply marker, mirroring the go-echo demo agent
// (a test asserts replies carry it, proving traffic did flow end to end).
const errMsgPrefix = "echo: "

// fakeAgent serves discovery cards plus JSON-RPC SendMessage /
// SendStreamingMessage in exactly the shapes the a2a-go SDK produces and
// consumes (a2a-go v2.6, A2A v1.0).
type fakeAgent struct {
	// baseURL is the server's own address; the card advertises it and the
	// gateway fetches the card through HTTP like any upstream.
	baseURL atomic.Value // string
}

// card is the discovery card payload (SDK AgentCard JSON keys).
func (a *fakeAgent) card() map[string]any {
	base := ""
	if v := a.baseURL.Load(); v != nil {
		base, _ = v.(string)
	}
	return map[string]any{
		"name":        "go-echo",
		"description": "bench echo agent (streams one artifact per word)",
		"supportedInterfaces": []map[string]any{
			{"url": base, "protocolBinding": "JSONRPC"},
		},
		"defaultInputModes":  []string{"text"},
		"defaultOutputModes": []string{"text"},
		"capabilities":       map[string]any{"streaming": true},
		"skills": []map[string]any{
			{"id": "echo", "name": "Echo", "description": "Echoes text", "tags": []string{"demo"}},
		},
	}
}

// serveCard answers GET/HEAD on the well-known path.
func (a *fakeAgent) serveCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, a.card())
}

// ServeHTTP dispatches discovery and the JSON-RPC data path.
func (a *fakeAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == cardPath {
		a.serveCard(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, `{"error":"unsupported_media_type"}`, http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"bad_request"}`, http.StatusBadRequest)
		return
	}
	var req struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			Message struct {
				ID    string `json:"messageId"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC == "" {
		http.Error(w, `{"error":"bad_request"}`, http.StatusBadRequest)
		return
	}
	text := textOf(req.Params.Message.Parts)
	switch req.Method {
	case "SendMessage":
		a.sendMessage(w, req.ID, text)
	case "SendStreamingMessage":
		a.streamMessage(w, req.ID, text)
	default:
		writeJSON(w, http.StatusOK, streamEnvelope(req.ID, map[string]any{
			"error": map[string]any{"code": -32601, "message": "method not found"},
		}))
	}
}

// textOf concatenates the text parts in order (the demo A2A contract).
func textOf(parts []struct {
	Text string `json:"text"`
}) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// streamEnvelope is the JSON-RPC response wrapper: `result` carries the
// A2A event union exactly as a2a-go renders a2a.StreamResponse.
func streamEnvelope(id any, result map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

// writeJSON is the small utility used by every handler above.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// sendMessage answers a message/send JSON-RPC with a single agent message —
// go-echo's non-streaming contract.
func (a *fakeAgent) sendMessage(w http.ResponseWriter, id any, text string) {
	writeJSON(w, http.StatusOK, streamEnvelope(id, map[string]any{
		"message": map[string]any{
			"messageId": "reply-" + strconv.FormatInt(time.Now().UnixNano(), 16),
			"role":      "ROLE_AGENT",
			"parts":     []map[string]any{{"text": errMsgPrefix + text}},
		},
	}))
}

// atomicTask counts task ids (plain unique strings; no UUID needed).
var atomicTask atomic.Int64

// writeSSEHeaders mirrors the SDK's streaming preamble.
func writeSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
}

// streamMessage replays the go-echo SSE lifecycle: a submitted task, a
// working status, one artifact event per word, then completed. Each event
// is one `data:` block closed by a blank line (WHATWG SSE as the SDK writes
// it). Without the demo agent's 40ms inter-chunk sleep, keeping raw loop
// throughput as the measurement — per-stream rate is the client's job.
func (a *fakeAgent) streamMessage(w http.ResponseWriter, id any, text string) {
	taskID := "task-" + strconv.FormatInt(atomicTask.Add(1), 10)
	ctxID := taskID + "-ctx"
	emit := func(result map[string]any) bool {
		data, _ := json.Marshal(streamEnvelope(id, result))
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false // client went away; never panic mid-SSE
		}
		w.(http.Flusher).Flush()
		return true
	}
	writeSSEHeaders(w)
	if !emit(map[string]any{
		"task": map[string]any{
			"id": taskID, "contextId": ctxID,
			"status": map[string]any{"state": "TASK_STATE_SUBMITTED"},
		},
	}) {
		return
	}
	if !emit(map[string]any{
		"statusUpdate": map[string]any{
			"taskId": taskID, "contextId": ctxID,
			"status": map[string]any{"state": "TASK_STATE_WORKING"},
		},
	}) {
		return
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		words = []string{""}
	}
	for i, word := range words {
		chunk := " " + word
		if i == 0 {
			chunk = errMsgPrefix + word
		}
		if !emit(map[string]any{
			"artifactUpdate": map[string]any{
				"taskId": taskID, "contextId": ctxID,
				"artifact": map[string]any{
					"artifactId": fmt.Sprintf("art-%d-%d", atomicTask.Load(), i),
					"parts":      []map[string]any{{"text": chunk}},
				},
			},
		}) {
			return
		}
	}
	_ = emit(map[string]any{
		"statusUpdate": map[string]any{
			"taskId": taskID, "contextId": ctxID,
			"status": map[string]any{"state": "TASK_STATE_COMPLETED"},
		},
	})
}

// start boots the agent behind httptest and registers cleanup. Card
// serving works only from the moment the URL is known; until then the
// refresher simply retries.
func (a *fakeAgent) start(tb testing.TB) *httptest.Server {
	tb.Helper()
	srv := httptest.NewServer(a)
	a.baseURL.Store(srv.URL)
	tb.Cleanup(srv.Close)
	return srv
}

// benchStack is the deployment under test: the real proxy handler between
// a plain Loopback HTTP agent and the bench driver, plus an admin-shape
// endpoint backed by the same Prometheus registry the gateway exposes at
// /metrics — the tests intentionally scrape via HTTP through the same
// parsing path the CLI uses against real gateways.
type benchStack struct {
	agent *httptest.Server
	gw    *httptest.Server
	admin *httptest.Server
	mtr   *metrics.Metrics
	tel   *telemetry.Telemetry
	store *storage.MemoryStore
}

// route returns the gateway agent route URL (-gateway-url for the driver).
func (s *benchStack) route() string { return s.gw.URL + "/a2a/echo" }

// adminURL is the metrics endpoint passed to the driver.
func (s *benchStack) adminURL() string { return s.admin.URL }

// scan reads one admin-shape /metrics scrape through the driver's parser.
func (s *benchStack) scan() (gatewaySnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return scrapeAdmin(ctx, s.admin.URL)
}

// startStack boots agent + gateway + admin exposition. config gets both
// the shipped defaults (card cache TTL, memory span store, sampling) and
// the raised stream cap tiers need.
func startStack(tb testing.TB, maxStreams int64) *benchStack {
	tb.Helper()
	agent := &fakeAgent{}
	agentSrv := agent.start(tb)
	cfg := agent.stackConfig(tb, maxStreams)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mtr := metrics.New()
	reg, err := registry.New(cfg)
	if err != nil {
		tb.Fatalf("registry: %v", err)
	}
	cache := registry.NewCardCache(reg, nil,
		cfg.CardCache.TTL.Std(), cfg.CardCache.StaleGrace.Std(),
		cfg.CardCache.MaxCardBytes, logger)
	cacheCtx, stopCache := context.WithCancel(context.Background())
	cache.Start(cacheCtx)

	// Production wires the in-process span store as the telemetry sink
	// (memory driver default) — kept here so load runs include it.
	store := storage.NewMemoryStore(cfg.Management.Storage.MemoryCapacity)
	tel, terr := telemetry.New(context.Background(), cfg.Telemetry, logger, mtr,
		telemetry.WithSink(store))
	if terr != nil {
		stopCache()
		tb.Fatalf("telemetry: %v", terr)
	}

	stack := &benchStack{agent: agentSrv, mtr: mtr, tel: tel, store: store}
	stack.gw = httptest.NewServer(proxy.New(cfg, reg, cache, mtr, tel, logger))
	promReg := mtr.Registry()
	stack.admin = httptest.NewServer(promhttp.HandlerFor(promReg, promhttp.HandlerOpts{Registry: promReg}))

	tb.Cleanup(func() {
		stack.gw.Close()
		stack.admin.Close()
		stopCache()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tel.Shutdown(shutdownCtx); err != nil {
			tb.Logf("telemetry shutdown: %v", err)
		}
		_ = store.Close()
	})
	return stack
}

// stackConfig seeds minimal-but-valid config with the runtime-resolved
// agent upstream, mirroring the integration harness pattern. maxStreams
// raises limits.max_concurrent_streams above the shipped default so the
// 1k/5k/10k tiers are not capped at 1024 live SSE responses.
func (a *fakeAgent) stackConfig(tb testing.TB, maxStreams int64) *config.Config {
	tb.Helper()
	agentURL := a.baseURL.Load().(string)
	yaml := fmt.Sprintf(`
listeners:
  a2a:
    addr: "127.0.0.1:0"
  admin:
    addr: "127.0.0.1:0"
telemetry:
  sample_ratio: 1
  queue_size: 65536
  batch_size: 1024
management:
  enabled: false
  storage:
    driver: memory
    memory_capacity: 4096
limits:
  max_concurrent_streams: %d
agents:
  - name: echo
    upstreams:
      - url: %s
`, maxStreams, agentURL)
	cfg, err := config.LoadBytes([]byte(yaml))
	if err != nil {
		tb.Fatalf("seed config: %v", err)
	}
	return cfg
}

// waitGaugeIdle polls the registry's active-streams gauge until it is
// within delta of zero, or fails the test after within.
func waitGaugeIdle(t *testing.T, s *benchStack, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	last := -1.0
	for time.Now().Before(deadline) {
		g, err := s.scan()
		if err == nil {
			last = g.ActiveStreams
			if last == 0 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("active streams gauge did not return to 0 within %s (last: %g)", within, last)
}
