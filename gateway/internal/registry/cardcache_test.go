package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// cardServer is a scripted upstream card endpoint for cache tests.
type cardServer struct {
	ts   *httptest.Server
	mu   sync.Mutex
	name string
	etag string
	fail bool
	hits int
}

func newCardServer(t *testing.T, initialName, initialETag string) *cardServer {
	t.Helper()
	cs := &cardServer{name: initialName, etag: initialETag}
	cs.ts = httptest.NewServer(http.HandlerFunc(cs.serve))
	t.Cleanup(cs.ts.Close)
	return cs
}

func (c *cardServer) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, WellKnownAgentCardPath) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hits++
	if c.fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if tag := r.Header.Get("If-None-Match"); c.etag != "" && tag == c.etag {
		w.Header().Set("ETag", c.etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if c.etag != "" {
		w.Header().Set("ETag", c.etag)
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{
		"name": %q,
		"supportedInterfaces": [{"url": "http://upstream-agent", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}],
		"defaultInputModes": ["text"],
		"defaultOutputModes": ["text"],
		"version": "0.1"
	}`, c.name)
}

func (c *cardServer) setFailure(fail bool) {
	c.mu.Lock()
	c.fail = fail
	c.mu.Unlock()
}

func (c *cardServer) setCard(name, etag string) {
	c.mu.Lock()
	c.name = name
	c.etag = etag
	c.mu.Unlock()
}

func (c *cardServer) hitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

// newRegistryForCardServer builds a one-agent registry pointing at the
// given upstream URL.
func newRegistryForCardServer(t *testing.T, upstreamURL string) *Registry {
	t.Helper()
	cfg, err := config.LoadBytes([]byte(fmt.Sprintf(`
listeners:
  a2a:
    addr: "127.0.0.1:8080"
  admin:
    addr: "127.0.0.1:8081"
agents:
  - name: echo
    upstreams:
      - url: %s
`, upstreamURL)))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	reg, err := New(cfg)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return reg
}

func TestCardCacheFetchRevalidateRefresh(t *testing.T) {
	cs := newCardServer(t, "Echo Agent", `"v1"`)
	reg := newRegistryForCardServer(t, cs.ts.URL)
	echo := reg.LookupKey("echo")

	// TTL 0 forces every Get to re-fetch, exercising all flows.
	cache := NewCardCache(reg, nil, 0, 50*time.Millisecond, 1<<20, testLogger())
	ctx := context.Background()

	res, err := cache.Get(ctx, echo)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if res.Source != "fetched" {
		t.Errorf("first source = %q, want fetched", res.Source)
	}
	if res.Card.Name != "Echo Agent" {
		t.Errorf("card name = %q", res.Card.Name)
	}
	if res.Card.SupportedInterfaces[0].ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("binding = %q", res.Card.SupportedInterfaces[0].ProtocolBinding)
	}

	// Revalidation: same ETag → 304, parsed card kept.
	before := cs.hitCount()
	res, err = cache.Get(ctx, echo)
	if err != nil {
		t.Fatalf("revalidate Get: %v", err)
	}
	if res.Source != "revalidated" {
		t.Errorf("second source = %q, want revalidated", res.Source)
	}
	if got := cs.hitCount(); got != before+1 {
		t.Errorf("expected exactly one new upstream hit, got %d -> %d", before, got)
	}

	// Upstream card content change with a new ETag → full refresh.
	cs.setCard("Echo Agent v2", `"v2"`)
	res, err = cache.Get(ctx, echo)
	if err != nil {
		t.Fatalf("refresh Get: %v", err)
	}
	if res.Source != "fetched" {
		t.Errorf("third source = %q, want fetched", res.Source)
	}
	if res.Card.Name != "Echo Agent v2" {
		t.Errorf("refreshed card name = %q", res.Card.Name)
	}
}

func TestCardCacheStaleWithinGraceAndErrorPast(t *testing.T) {
	cs := newCardServer(t, "Echo Agent", `"v1"`)
	reg := newRegistryForCardServer(t, cs.ts.URL)
	echo := reg.LookupKey("echo")

	const grace = 60 * time.Millisecond
	cache := NewCardCache(reg, nil, 0, grace, 1<<20, testLogger())
	ctx := context.Background()

	if _, err := cache.Get(ctx, echo); err != nil {
		t.Fatalf("seed Get: %v", err)
	}

	// Upstream dies: within the grace the cached card is served stale.
	cs.setFailure(true)
	res, staleErr := cache.Get(ctx, echo)
	if staleErr != nil {
		t.Fatalf("expected stale card within grace, got error: %v", staleErr)
	}
	if res == nil || res.Source != "stale" {
		t.Fatalf("expected stale source, got %+v", res)
	}
	if res.Card.Name != "Echo Agent" {
		t.Errorf("stale card name drifted: %q", res.Card.Name)
	}

	// Past the grace the same outage surfaces the failure to callers.
	time.Sleep(grace + 30*time.Millisecond)
	res, staleErr = cache.Get(ctx, echo)
	if staleErr == nil {
		t.Fatalf("expected error after stale grace, got card %+v", res)
	}
	var cardErr *CardError
	if !errors.As(staleErr, &cardErr) {
		t.Fatalf("expected *CardError, got %T", staleErr)
	}
}
