// Stream-cap accounting tests: with limits.max_concurrent_streams at its
// limit, the extra SSE response is rejected with 503 (stream_cap_exhausted)
// and both the rejection counter and the active-streams gauge stay exact —
// a rejected request must not hold a semaphore token or an active-stream
// gauge entry it would never release (GW-2, PRD §7).
package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

const (
	streamAgentName = "up"
	streamGatePath  = "/a2a/" + streamAgentName + "/gate"
)

// holdStreamUpstream answers every proxied request with an SSE body that
// writes one flushed chunk, then holds until released (or the connection
// goes away). Requests queued behind the stream cap therefore park against
// a client-visible first chunk, not against a sleep.
type holdStreamUpstream struct {
	srv         *httptest.Server
	releaseOnce sync.Once
	releaseCh   chan struct{}
}

func newHoldStreamUpstream(t *testing.T) *holdStreamUpstream {
	t.Helper()
	u := &holdStreamUpstream{releaseCh: make(chan struct{})}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, registry.WellKnownAgentCardPath) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"name":"%s","version":"1.0","supportedInterfaces":[{"url":%q,"protocolBinding":"JSONRPC"}]}`,
				streamAgentName, r.Host)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: first\n\n")
		if f != nil {
			f.Flush()
		}
		select {
		case <-r.Context().Done(): // gateway dropped the rejected proxy stream
		case <-u.releaseCh:
		case <-time.After(10 * time.Second): // absolute bound: never wedge a test
		}
		_, _ = io.WriteString(w, "data: done\n\n")
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *holdStreamUpstream) release() { u.releaseOnce.Do(func() { close(u.releaseCh) }) }

// newStreamCapGateway is one real Handler on an httptest server with a
// single-slot stream cap, the finished span store unused (export disabled).
func newStreamCapGateway(t *testing.T, upstreamURL string, cap int64) (*httptest.Server, *metrics.Metrics) {
	t.Helper()
	doc := fmt.Sprintf(`
listeners:
  a2a:
    addr: "127.0.0.1:0"
  admin:
    addr: "127.0.0.1:0"
limits:
  max_concurrent_streams: %d
agents:
  - name: %s
    upstreams:
      - url: %q
`, cap, streamAgentName, upstreamURL)
	cfg, err := config.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatalf("load stream-cap config: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mtr := metrics.New()
	reg, err := registry.New(cfg)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	cache := registry.NewCardCache(
		reg, nil,
		cfg.CardCache.TTL.Std(), cfg.CardCache.StaleGrace.Std(), cfg.CardCache.MaxCardBytes,
		logger,
	)
	// Nil exporter mirrors an empty otlp_endpoint: propagation works,
	// nothing is exported, no background export goroutine runs.
	tel, err := telemetry.NewWithExporter(context.Background(), cfg.Telemetry, logger, mtr, nil)
	if err != nil {
		t.Fatalf("build telemetry: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tel.Shutdown(ctx)
	})

	base := httptest.NewServer(New(cfg, reg, cache, mtr, tel, logger))
	t.Cleanup(base.Close)
	return base, mtr
}

// readFirstChunk issues the request, reports the first body line once its
// headers have crossed the gateway (the cap token is held from that point
// on), and reports the rest after the upstream releases.
func readFirstChunk(url string, started chan<- streamChunk, done chan<- streamChunk) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(`{}`))
	if err != nil {
		done <- streamChunk{err: err}
		return
	}
	r := bufio.NewReader(resp.Body)
	first, ferr := r.ReadString('\n')
	started <- streamChunk{status: resp.StatusCode, line: first, err: ferr}
	rest, rerr := r.ReadBytes(0) // the response stays open until release
	_ = resp.Body.Close()
	done <- streamChunk{status: resp.StatusCode, line: first, rest: rest, err: rerr}
}

type streamChunk struct {
	status int
	line   string
	rest   []byte
	err    error
}

func receiveChunk(t *testing.T, ch <-chan streamChunk, what string) streamChunk {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never completed within 5s", what)
		return streamChunk{}
	}
}

// TestStreamCapRejectsExtraStreamAndAccountingStaysExact pins the
// rejection contract and the gauge/semaphore hygiene at the cap boundary:
// the capped stream finishes normally afterwards, and nothing leaks.
func TestStreamCapRejectsExtraStreamAndAccountingStaysExact(t *testing.T) {
	up := newHoldStreamUpstream(t)
	base, mtr := newStreamCapGateway(t, up.srv.URL, 1)
	url := base.URL + streamGatePath

	started := make(chan streamChunk, 1)
	done := make(chan streamChunk, 1)
	go readFirstChunk(url, started, done)
	first := receiveChunk(t, started, "the capped stream's first chunk")
	if first.err != nil {
		t.Fatalf("capped stream failed before the second request: %v", first.err)
	}
	if first.status != http.StatusOK || strings.TrimRight(first.line, "\n") != "data: first" {
		t.Fatalf("capped stream start = %d %q, want 200 + %q", first.status, first.line, "data: first")
	}

	// A second concurrent stream over the cap of 1: rejected with the
	// typed 503 body, never reaching the active-streams gauge.
	rejected, err := (&http.Client{Timeout: 5 * time.Second}).Post(url, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("over-cap request failed: %v", err)
	}
	body, _ := io.ReadAll(rejected.Body)
	_ = rejected.Body.Close()
	if rejected.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "stream_cap_exhausted") {
		t.Errorf("over-cap response = %d %q, want 503 stream_cap_exhausted", rejected.StatusCode, string(body))
	}
	if got := testutil.ToFloat64(mtr.StreamCapRejectionsTotal.WithLabelValues(streamAgentName)); got != 1 {
		t.Errorf("stream_cap_rejections_total = %v after one rejection, want 1", got)
	}
	if got := testutil.ToFloat64(mtr.ActiveStreams.WithLabelValues(streamAgentName)); got != 1 {
		t.Errorf("active_streams = %v while only one stream is live (a rejected request must not hold a token)", got)
	}

	up.release()
	last := receiveChunk(t, done, "the capped stream")
	// A close-delimited SSE body always ends in EOF once the upstream
	// handler finishes; only other errors are failures.
	if last.err != nil && !errors.Is(last.err, io.EOF) {
		t.Fatalf("capped stream failed during drain: %v", last.err)
	}
	if last.status != http.StatusOK || !strings.Contains(string(last.rest), "data: done") {
		t.Errorf("capped stream end = %d %q, want 200 with the trailing chunk delivered", last.status, string(last.rest))
	}
	if got := testutil.ToFloat64(mtr.StreamCapRejectionsTotal.WithLabelValues(streamAgentName)); got != 1 {
		t.Errorf("stream_cap_rejections_total = %v after the drain, want 1", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for testutil.ToFloat64(mtr.ActiveStreams.WithLabelValues(streamAgentName)) != 0 {
		if time.Now().After(deadline) {
			t.Errorf("active_streams = %v after the stream closed; the rejected request leaked a gauge entry",
				testutil.ToFloat64(mtr.ActiveStreams.WithLabelValues(streamAgentName)))
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
}
