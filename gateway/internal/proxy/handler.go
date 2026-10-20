// Package proxy implements the A2A transparent forwarding core: routing,
// agent card rewriting, trace-context propagation and metrics-instrumented
// reverse proxying.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/cost"
	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

// context keys store per-request routing decisions for the shared
// ReverseProxy's Rewrite hook.
type ctxKey int

const (
	ctxOutboundURL ctxKey = iota
	ctxHandoff
)

// Handler is the A2A data-path HTTP handler:
//
//	{prefix}/{key}/                    proxied (JSON-RPC posts at the root)
//	{prefix}/{key}/{rest}              proxied (HTTP+JSON binding paths)
//	{prefix}/{key}/.well-known/agent-card.json   rewritten discovery card
//
// `key` is an agent name or alias. Backend failures surface as 502 with a
// JSON body; unknown routes as 404.
type Handler struct {
	reg       *registry.Registry
	cache     *registry.CardCache
	mtr       *metrics.Metrics
	tel       *telemetry.Telemetry
	logger    *slog.Logger
	requestTO time.Duration
	maxBody   int64
	streamCap int64
	transport http.RoundTripper
	rp        *httputil.ReverseProxy
	pool      bufferPool
	streams   chan struct{} // semaphore when streamCap > 0
	knownBase string        // configured public_base_url ('' = derive per request)
	routes    []route       // ordered: longer prefixes first
	prices    cost.Table    // price table for the per-span cost (CO-2)
}

// Option customizes the handler.
type Option func(*Handler)

// WithPriceTable gives the handler the price table used to turn reported
// token usage into a per-span cost (CO-2). Without a table the cost stays
// unknown, which is reported as such.
func WithPriceTable(prices cost.Table) Option {
	return func(h *Handler) { h.prices = prices }
}

// route binds a listen-path prefix to its registry entry.
type route struct {
	raw   string // configured path (canonical case, used for URLs)
	entry *registry.Entry
}

// New builds the handler: routing table, reverse proxy, upstream transport
// with GW-6 timeouts, and Prometheus instruments.
func New(cfg *config.Config, reg *registry.Registry, cache *registry.CardCache, mtr *metrics.Metrics, tel *telemetry.Telemetry, logger *slog.Logger, opts ...Option) *Handler {
	if tel == nil {
		// Defensive: a disabled provider still mints valid trace IDs and
		// propagates W3C context, so ad-hoc uses never lose M1 behavior.
		tel, _ = telemetry.New(context.Background(), config.Telemetry{}, logger, mtr)
	}
	h := &Handler{
		reg:       reg,
		cache:     cache,
		mtr:       mtr,
		tel:       tel,
		logger:    logger,
		requestTO: cfg.Limits.Timeouts.Request.Std(),
		maxBody:   cfg.Limits.MaxRequestBodyBytes,
		streamCap: cfg.Limits.MaxConcurrentStreams,
		transport: newTransport(cfg),
	}
	for _, opt := range opts {
		opt(h)
	}
	h.knownBase = strings.TrimSuffix(cfg.Listeners.A2A.PublicBaseURL, "/")
	h.pool = bufferPool{p: sync.Pool{New: func() any {
		buf := make([]byte, 32*1024)
		return &buf
	}}}
	for _, entry := range reg.Entries() {
		h.routes = append(h.routes, route{raw: entry.RoutePath, entry: entry})
		for _, alias := range entry.AliasPaths {
			h.routes = append(h.routes, route{raw: alias, entry: entry})
		}
	}
	// Longer prefixes first (list is tiny; insertion sort is clear).
	for i := 1; i < len(h.routes); i++ {
		for j := i; j > 0 && len(h.routes[j].raw) > len(h.routes[j-1].raw); j-- {
			h.routes[j], h.routes[j-1] = h.routes[j-1], h.routes[j]
		}
	}
	if h.streamCap > 0 {
		h.streams = make(chan struct{}, h.streamCap)
	}
	h.rp = &httputil.ReverseProxy{
		Rewrite:        h.rewrite,
		Transport:      h.transport,
		FlushInterval:  -1, // stream-friendly: flush on every write (GW-2)
		BufferPool:     &h.pool,
		ModifyResponse: h.modifyResponse,
		ErrorHandler:   h.errorHandler,
	}
	mtr.ActiveStreamsLimit.Set(float64(cfg.Limits.MaxConcurrentStreams))
	mtr.RegistrySize.Set(float64(reg.Len()))
	return h
}

// newTransport builds the upstream transport: connect and first-byte
// timeouts from limits.timeouts; connection pooling for hot agent fan-out.
func newTransport(cfg *config.Config) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: cfg.Limits.Timeouts.Connect.Std()}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: cfg.Limits.Timeouts.ResponseHeader.Std(),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          512,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
	}
}

type bufferPool struct{ p sync.Pool }

func (b *bufferPool) Get() []byte {
	bp, _ := b.p.Get().(*[]byte)
	if bp == nil {
		return make([]byte, 32*1024)
	}
	return *bp
}

func (b *bufferPool) Put(x []byte) {
	if len(x) == 32*1024 {
		bp := &x
		b.p.Put(bp)
	}
}

// bindingOf infers the A2A binding a request is using, from its path shape.
// A2A JSON-RPC requests POST to the interface URL root; HTTP+JSON (REST)
// binding requests always carry suffixes (/message:send, /tasks/..., ...).
// When upstreams are annotated with binding hints, matching ones win
// (registry.PickUpstream).
func bindingOf(suffix string) a2a.TransportProtocol {
	if suffix == "" || suffix == "/" {
		return a2a.TransportProtocolJSONRPC
	}
	return a2a.TransportProtocolHTTPJSON
}

// routeMatch carries the resolution result for one inbound request.
type routeMatch struct {
	entry  *registry.Entry
	raw    string // configured path as written (used for card URLs)
	suffix string // inbound path after the route boundary ("" or "/...")
}

// resolve matches the request's raw escaped path against route prefixes.
// Route paths contain URL-safe segments only (no percent escapes), so raw
// prefix comparison on the escaped path is sound. Matching is
// case-insensitive for operator convenience; card URLs always use the
// configured casing.
func (h *Handler) resolve(escapedPath string) *routeMatch {
	for i := range h.routes {
		prefix := h.routes[i].raw
		if len(escapedPath) < len(prefix) ||
			!strings.EqualFold(escapedPath[:len(prefix)], prefix) {
			continue
		}
		if len(escapedPath) > len(prefix) && escapedPath[len(prefix)] != '/' {
			continue
		}
		return &routeMatch{
			entry:  h.routes[i].entry,
			raw:    prefix,
			suffix: escapedPath[len(prefix):],
		}
	}
	return nil
}

// ServeHTTP dispatches A2A traffic. The A2A listener only ever sees this
// handler, so admin endpoints cannot be reached on the data path (PRD §4.6).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	match := h.resolve(r.URL.EscapedPath())
	if match == nil {
		h.writeJSONError(w, http.StatusNotFound, "unknown_agent",
			"no agent registered under this path")
		return
	}
	if h.maxBody > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.maxBody)
	}

	if match.suffix == registry.WellKnownAgentCardPath {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			h.writeJSONError(w, http.StatusMethodNotAllowed,
				"method_not_allowed", "agent card is served over GET/HEAD only")
			return
		}
		h.serveCard(w, r, match)
		return
	}
	h.forward(w, r, match)
}

// writeJSONError writes a small JSON error body; never logs payloads.
func (h *Handler) writeJSONError(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := json.Marshal(map[string]string{"error": kind, "message": msg})
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		h.logger.Debug("error body write failed", "err", err)
	}
}

// context keys store the routing label for reuse by nested hooks
// (errorHandler, modifyResponse).
type ctxAgentKey struct{}

// withAgent stores the agent label on the request context.
func withAgent(ctx context.Context, agent string) context.Context {
	return context.WithValue(ctx, ctxAgentKey{}, agent)
}

// agentFromContext extracts the agent label for metrics attribution.
func agentFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxAgentKey{}).(string); ok {
		return v
	}
	return "unknown"
}

// proxyWriter records status and byte counts while preserving Flush (for
// ReverseProxy's immediate-flush SSE behavior).
type proxyWriter struct {
	http.ResponseWriter
	flusher http.Flusher
	code    int
	bytes   int64
}

func newProxyWriter(w http.ResponseWriter) *proxyWriter {
	pw := &proxyWriter{ResponseWriter: w}
	if f, ok := w.(http.Flusher); ok {
		pw.flusher = f
	}
	return pw
}

func (pw *proxyWriter) WriteHeader(code int) {
	if pw.code == 0 {
		pw.code = code
	}
	pw.ResponseWriter.WriteHeader(code)
}

func (pw *proxyWriter) Write(b []byte) (int, error) {
	n, err := pw.ResponseWriter.Write(b)
	pw.bytes += int64(n)
	return n, err
}

func (pw *proxyWriter) Flush() {
	if pw.flusher != nil {
		pw.flusher.Flush()
	}
}

// status returns the written status code (200 if never written: ReverseProxy
// guarantees a WriteHeader call, so reaching here means a 2xx shortcut path).
func (pw *proxyWriter) status() int {
	if pw.code == 0 {
		return http.StatusOK
	}
	return pw.code
}
