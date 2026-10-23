package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	resilience "github.com/kyurem-project/kyurem/gateway/internal/failure"
	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFailureTransportFailsOverOnRetryableStatus(t *testing.T) {
	calls := map[string]int{}
	transport := &failureTransport{
		manager: newTestFailureManager(2, 5),
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls[req.URL.Host]++
			if req.URL.Host == "primary" {
				return testResponse(req, http.StatusServiceUnavailable, "retry"), nil
			}
			return testResponse(req, http.StatusOK, "ok"), nil
		}),
	}
	req := requestWithPlan(t, http.MethodGet, "", attemptPlan{
		agent: "echo", retryable: true,
		targets: []attemptTarget{{key: "primary", url: mustURL(t, "http://primary")}, {key: "backup", url: mustURL(t, "http://backup")}},
	})
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || calls["primary"] != 1 || calls["backup"] != 1 {
		t.Fatalf("status=%d calls=%v, want 200 and one attempt per upstream", resp.StatusCode, calls)
	}
}

func TestGatewayRetriesKeyedA2ARequestAcrossUpstreams(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"jsonrpc":"2.0","method":"message/send"}` {
			t.Errorf("backup body=%q", body)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{}}`)
	}))
	defer backup.Close()

	doc := fmt.Sprintf(`
listeners:
  a2a:
    addr: "127.0.0.1:0"
  admin:
    addr: "127.0.0.1:0"
failure_handling:
  max_attempts: 2
  retry_budget:
    max_retries: 4
    window: 1m
  circuit_breaker:
    failure_threshold: 3
    open_duration: 1s
agents:
  - name: echo
    upstreams:
      - url: %q
        binding: JSONRPC
      - url: %q
        binding: JSONRPC
`, primary.URL, backup.URL)
	cfg, err := config.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mtr := metrics.New()
	cache := registry.NewCardCache(reg, nil, cfg.CardCache.TTL.Std(), cfg.CardCache.StaleGrace.Std(), cfg.CardCache.MaxCardBytes, logger)
	tel, err := telemetry.NewWithExporter(context.Background(), cfg.Telemetry, logger, mtr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tel.Shutdown(ctx)
	})
	gateway := httptest.NewServer(New(cfg, reg, cache, mtr, tel, logger, WithFailureHandling(cfg.FailureHandling)))
	defer gateway.Close()

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/a2a/echo", strings.NewReader(`{"jsonrpc":"2.0","method":"message/send"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "a2a-call-1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || primaryCalls.Load() != 1 || backupCalls.Load() != 1 {
		t.Fatalf("status=%d primary=%d backup=%d, want 200 and one request per upstream", response.StatusCode, primaryCalls.Load(), backupCalls.Load())
	}
}

func TestFailureTransportDoesNotRetryUnkeyedPost(t *testing.T) {
	calls := 0
	transport := &failureTransport{
		manager: newTestFailureManager(2, 5),
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return testResponse(req, http.StatusServiceUnavailable, "no retry"), nil
		}),
	}
	req := requestWithPlan(t, http.MethodPost, "payload", attemptPlan{
		agent: "echo", retryable: false,
		targets: []attemptTarget{{key: "primary", url: mustURL(t, "http://primary")}, {key: "backup", url: mustURL(t, "http://backup")}},
	})
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || calls != 1 {
		t.Fatalf("status=%d calls=%d, want one 503 attempt", resp.StatusCode, calls)
	}
}

func TestIdempotentPostBodyCanBeReplayedForFailover(t *testing.T) {
	var bodies []string
	transport := &failureTransport{
		manager: newTestFailureManager(2, 5),
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			bodies = append(bodies, string(body))
			status := http.StatusOK
			if req.URL.Host == "primary" {
				status = http.StatusBadGateway
			}
			return testResponse(req, status, "response"), nil
		}),
	}
	req := requestWithPlan(t, http.MethodPost, "payload", attemptPlan{
		agent: "echo", retryable: false,
		targets: []attemptTarget{{key: "primary", url: mustURL(t, "http://primary")}, {key: "backup", url: mustURL(t, "http://backup")}},
	})
	req.Header.Set("Idempotency-Key", "request-42")
	plan := req.Context().Value(ctxFailurePlan).(attemptPlan)
	plan.retryable = prepareRetry(req, 32)
	req = req.WithContext(context.WithValue(req.Context(), ctxFailurePlan, plan))
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(bodies) != 2 || bodies[0] != "payload" || bodies[1] != "payload" {
		t.Fatalf("status=%d bodies=%q, want successful replay of same payload", resp.StatusCode, bodies)
	}
}

func TestOpenCircuitsReturnServiceUnavailable(t *testing.T) {
	m := resilience.New(config.FailureHandling{
		MaxAttempts:    2,
		RetryBudget:    config.RetryBudget{MaxRetries: 1, Window: config.Duration(time.Minute)},
		CircuitBreaker: config.CircuitBreaker{FailureThreshold: 1, OpenDuration: config.Duration(time.Second)},
	})
	if allowed, _ := m.Begin("primary", "echo", false); !allowed {
		t.Fatal("first attempt was denied")
	}
	m.Complete("primary", false, false)
	transport := &failureTransport{manager: m, base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("open circuit reached upstream transport")
		return nil, nil
	})}
	req := requestWithPlan(t, http.MethodGet, "", attemptPlan{
		agent: "echo", retryable: true,
		targets: []attemptTarget{{key: "primary", url: mustURL(t, "http://primary")}},
	})
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", resp.StatusCode)
	}
}

func TestClientCancellationDoesNotRetryOrOpenCircuit(t *testing.T) {
	m := resilience.New(config.FailureHandling{
		MaxAttempts:    2,
		RetryBudget:    config.RetryBudget{MaxRetries: 4, Window: config.Duration(time.Minute)},
		CircuitBreaker: config.CircuitBreaker{FailureThreshold: 1, OpenDuration: config.Duration(time.Second)},
	})
	calls := 0
	req := requestWithPlan(t, http.MethodGet, "", attemptPlan{
		agent: "echo", retryable: true,
		targets: []attemptTarget{{key: "primary", url: mustURL(t, "http://primary")}, {key: "backup", url: mustURL(t, "http://backup")}},
	})
	ctx, cancel := context.WithCancel(req.Context())
	transport := &failureTransport{manager: m, base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, req.Context().Err()
	})}
	req = req.WithContext(ctx)
	_, err := transport.RoundTrip(req)
	if err != context.Canceled || calls != 1 {
		t.Fatalf("error=%v calls=%d, want one canceled upstream attempt", err, calls)
	}
	if allowed, _ := m.Begin("primary", "echo", false); !allowed {
		t.Fatal("client cancellation incorrectly opened the upstream circuit")
	}
}

func TestPrepareRetryRequiresKeyAndBoundsBody(t *testing.T) {
	req := requestWithPlan(t, http.MethodPost, "body", attemptPlan{})
	req.GetBody = nil
	if prepareRetry(req, 16) {
		t.Fatal("unkeyed POST must not be retried")
	}
	req.Header.Set("Idempotency-Key", "request-1")
	if !prepareRetry(req, 16) {
		t.Fatal("keyed small POST should be retryable")
	}
	replay, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	body, _ := io.ReadAll(replay)
	if string(body) != "body" {
		t.Fatalf("replayed body=%q", body)
	}

	large := requestWithPlan(t, http.MethodPost, "large-body", attemptPlan{})
	large.GetBody = nil
	large.Header.Set("Idempotency-Key", "request-2")
	if prepareRetry(large, 2) {
		t.Fatal("body larger than retry cap must stay one-shot")
	}
	body, err = io.ReadAll(large.Body)
	if err != nil || string(body) != "large-body" {
		t.Fatalf("large one-shot request body was not restored: %q, %v", body, err)
	}
}

func newTestFailureManager(attempts, retries int) *resilience.Manager {
	return resilience.New(config.FailureHandling{
		MaxAttempts:    attempts,
		RetryBudget:    config.RetryBudget{MaxRetries: retries, Window: config.Duration(time.Minute)},
		CircuitBreaker: config.CircuitBreaker{FailureThreshold: 5, OpenDuration: config.Duration(time.Second)},
	})
}

func requestWithPlan(t *testing.T, method, body string, plan attemptPlan) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "http://primary", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return req.WithContext(context.WithValue(req.Context(), ctxFailurePlan, plan))
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func testResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{Status: http.StatusText(status), StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}
}
