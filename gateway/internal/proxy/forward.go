package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// forward proxies one A2A request to its selected upstream. The hot path
// performs no body buffering: ReverseProxy streams with pooled buffers and
// immediate flushing (GW-2), while per-request state (agent label, resolved
// URL) rides the cloned request itself.
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, match *routeMatch) {
	binding := bindingOf(match.suffix)
	upstream := match.entry.PickUpstream(binding)
	if upstream == nil {
		h.writeJSONError(w, http.StatusBadGateway, "no_upstream",
			"agent has no upstream endpoint configured")
		return
	}

	outURL, err := buildUpstreamURL(upstream.URL, match.suffix)
	if err != nil {
		h.writeJSONError(w, http.StatusBadGateway, "bad_upstream_url", err.Error())
		return
	}

	agent := match.entry.Name
	start := time.Now()

	ctx := withAgent(r.Context(), agent)
	if h.requestTO > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.requestTO)
		defer cancel()
	}

	// Byte-in accounting without buffering: wrap the stream once; both the
	// clone below and ReverseProxy read through it.
	if r.Body != nil {
		r.Body = &countingBody{ReadCloser: r.Body}
	}

	// The outbound request is a full clone of the inbound request with URL
	// and trace headers rewritten; ReverseProxy handles hop-by-hop rules.
	out := r.Clone(ctx)
	out.URL = outURL
	out.Host = outURL.Host
	propagateTraceContext(out.Header, r.Header)

	pw := newProxyWriter(w)
	h.rp.ServeHTTP(pw, out)

	if cb, ok := r.Body.(*countingBody); ok {
		h.mtr.ObserveBytes(agent, "in", cb.n)
	}
	h.mtr.ObserveBytes(agent, "out", pw.bytes)
	h.mtr.ObserveRequest(agent, string(binding), time.Since(start).Seconds(), pw.status())
}

// buildUpstreamURL composes the outbound URL: upstream base + inbound path
// suffix, preserving the suffix's raw escaping (task IDs may contain %XX
// and REST verbs appear as ':cancel'/':subscribe' path suffixes).
func buildUpstreamURL(upstreamBase, suffix string) (*url.URL, error) {
	base, err := url.Parse(upstreamBase)
	if err != nil {
		return nil, fmt.Errorf("upstream URL is not a valid URL: %w", err)
	}
	if suffix == "" || suffix == "/" {
		return base, nil
	}
	joined := &url.URL{
		Scheme:   base.Scheme,
		Host:     base.Host,
		Path:     strings.TrimSuffix(base.Path, "/") + suffix,
		RawQuery: base.RawQuery,
	}
	if base.RawPath != "" {
		joined.RawPath = strings.TrimSuffix(base.EscapedPath(), "/") + suffix
	}
	return joined, nil
}

// rewrite is the shared ReverseProxy hook: fill in standard forwarding
// headers. URL routing was applied by forward() on the cloned request.
func (h *Handler) rewrite(pr *httputil.ProxyRequest) {
	pr.SetXForwarded()
}

// proxyError carries a pre-classified proxy failure to errorHandler so the
// gateway answers with the correct status (PRD §4.5 spirit: failure modes
// are observable and distinguishable).
type proxyError struct {
	status int
	reason string
	err    error
}

func (e *proxyError) Error() string {
	return fmt.Sprintf("%s: %v", e.reason, e.err)
}

// modifyResponse instruments proxied responses: active-stream accounting
// for SSE responses, bounded by the configured stream cap. A cap overrun
// surfaces as 503 via the typed error path.
func (h *Handler) modifyResponse(resp *http.Response) error {
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil
	}
	agent := agentFromContext(resp.Request.Context())
	h.mtr.ActiveStreams.WithLabelValues(agent).Inc()
	if h.streamCap <= 0 {
		resp.Body = &streamTrackedBody{
			ReadCloser: resp.Body,
			release:    func() { h.mtr.ActiveStreams.WithLabelValues(agent).Dec() },
		}
		return nil
	}
	select {
	case h.streams <- struct{}{}:
		resp.Body = &streamTrackedBody{
			ReadCloser: resp.Body,
			release: func() {
				<-h.streams
				h.mtr.ActiveStreams.WithLabelValues(agent).Dec()
			},
		}
		return nil
	default:
		return &proxyError{
			status: http.StatusServiceUnavailable,
			reason: "stream_cap_exhausted",
			err:    fmt.Errorf("live SSE responses exceeded limits.max_concurrent_streams (%d)", h.streamCap),
		}
	}
}

// streamTrackedBody releases the stream semaphore exactly once when
// ReverseProxy closes the upstream response body (it always does, even on
// cancellation, so gauges cannot leak).
type streamTrackedBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (s *streamTrackedBody) Close() error {
	s.once.Do(s.release)
	return s.ReadCloser.Close()
}

// errorHandler runs when proxying fails before any status was written:
// upstream unreachable, refused, timed out, or rejected by a limit.
func (h *Handler) errorHandler(w http.ResponseWriter, r *http.Request, proxyErr error) {
	var typedErr *proxyError
	if errors.As(proxyErr, &typedErr) {
		h.logger.Debug("proxy request rejected",
			"agent", agentFromContext(r.Context()),
			"reason", typedErr.reason, "err", typedErr.err)
		h.writeJSONError(w, typedErr.status, typedErr.reason, typedErr.err.Error())
		return
	}

	reason := "upstream_error"
	switch {
	case errors.Is(proxyErr, context.DeadlineExceeded):
		reason = "upstream_timeout"
	case errors.Is(proxyErr, context.Canceled):
		reason = "client_canceled"
	case strings.Contains(proxyErr.Error(), "request body too large"):
		h.logger.Debug("proxy request rejected", "agent", agentFromContext(r.Context()), "reason", "body_too_large")
		h.writeJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			"proxied request body exceeds limits.max_request_body_bytes")
		return
	}
	// Never log payloads: error text already excludes body bytes.
	h.logger.Debug("proxy error",
		"agent", agentFromContext(r.Context()), "reason", reason, "err", proxyErr)
	h.writeJSONError(w, http.StatusBadGateway, reason,
		"upstream agent is unreachable or failed before producing a response")
}

// countingBody counts bytes streamed through the request body without
// buffering any of them (PRD §7: no per-request allocations that scale
// with body size beyond the pool ReverseProxy already uses).
type countingBody struct {
	io.ReadCloser
	n int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}
