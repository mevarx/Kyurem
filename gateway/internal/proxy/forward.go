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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

// handoffState carries one handoff span and the bounded payload taps used
// to fill TR-3 attributes when the exchange ends. All fields are touched by
// the ReverseProxy goroutine that owns the request.
type handoffState struct {
	span        trace.Span
	start       time.Time
	requestTap  *telemetry.BodyTap
	responseTap *telemetry.BodyTap
	bytesIn     int64
	bytesOut    int64
	status      int
	errorType   string
	err         error
}

func (s *handoffState) fail(status int, reason string, err error) {
	s.status = status
	s.errorType = reason
	s.err = err
}

func handoffStateFromContext(ctx context.Context) *handoffState {
	st, _ := ctx.Value(ctxHandoff).(*handoffState)
	return st
}

// forward proxies one A2A request to its selected upstream. The hot path
// performs no body buffering: ReverseProxy streams with pooled buffers and
// immediate flushing (GW-2), while per-request state (agent label, resolved
// URL, trace span) rides the cloned request itself.
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, match *routeMatch) {
	agent := match.entry.Name
	start := time.Now()

	// Caller authentication runs before anything is forwarded. Policy uses
	// this identity, never the untrusted X-Kyurem-Caller hint (TP-5).
	identity, authErr := h.identify(r)

	// Extract the caller's W3C context before starting the hop span. A
	// missing or invalid header yields an empty context, so StartSpan mints
	// a fresh gateway root exactly like M1 (ADR 0003).
	inbound := h.tel.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	caller := h.tel.Caller(inbound, r.Header.Get(telemetry.CallerHeader))
	ctx, span := h.tel.StartSpan(inbound, telemetry.InvokeAgentSpanName(agent),
		attribute.String(telemetry.AttrOperationName, telemetry.OperationInvokeAgent),
		attribute.String(telemetry.AttrProviderName, telemetry.ProviderKyurem),
		attribute.String(telemetry.AttrAgentName, agent),
		attribute.String(telemetry.AttrCallerAgent, caller.Agent),
		attribute.String(telemetry.AttrCalleeAgent, agent),
		attribute.Int(telemetry.AttrHopIndex, caller.Hop),
		attribute.String(telemetry.AttrCardVerified, telemetry.CardVerifiedUnchecked),
		attribute.String(telemetry.AttrAuthMethod, identity.Method),
		attribute.String(telemetry.AttrAuthCaller, identity.Caller),
		attribute.Bool(telemetry.AttrTokensKnown, false),
		attribute.String(telemetry.AttrHTTPRequestMeth, r.Method),
		attribute.String(telemetry.AttrURLPath, r.URL.Path),
	)
	// Remember at start, not end: nested agents call back through the
	// gateway while this span is still open.
	h.tel.Remember(span.SpanContext(), agent, caller.Hop)

	st := &handoffState{span: span, start: start}
	ctx = context.WithValue(ctx, ctxHandoff, st)
	defer h.finishHandoff(st)

	ctx = withAgent(ctx, agent)
	if authErr != nil {
		h.rejectAuth(r, w, st, agent, authErr)
		return
	}
	if rej := h.checkTrust(ctx, match, st); rej != nil {
		h.reject(w, st, rej)
		return
	}
	if rej := h.checkPolicy(r, match, st, identity); rej != nil {
		h.reject(w, st, rej)
		return
	}
	if h.requestTO > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.requestTO)
		defer cancel()
	}

	binding := bindingOf(match.suffix)
	upstream := match.entry.PickUpstream(binding)
	if upstream == nil {
		st.fail(http.StatusBadGateway, "no_upstream", nil)
		h.writeJSONError(w, http.StatusBadGateway, "no_upstream",
			"agent has no upstream endpoint configured")
		return
	}

	outURL, err := buildUpstreamURL(upstream.URL, match.suffix)
	if err != nil {
		st.fail(http.StatusBadGateway, "bad_upstream_url", err)
		h.writeJSONError(w, http.StatusBadGateway, "bad_upstream_url", err.Error())
		return
	}

	// Byte-in accounting with a bounded prefix tap: streaming is unchanged,
	// and only the first 64 KiB are retained for task/context extraction.
	if r.Body != nil {
		tap := telemetry.NewBodyTap(r.Body, telemetry.MaxTapBytes)
		r.Body = tap
		st.requestTap = tap
	}

	// The outbound request is a full clone of the inbound request with URL
	// and trace headers rewritten; ReverseProxy handles hop-by-hop rules.
	out := r.Clone(ctx)
	out.URL = outURL
	out.Host = outURL.Host
	out.Header.Del("traceparent")
	out.Header.Del("tracestate")
	out.Header.Del(SkillHeader)
	h.tel.Inject(ctx, propagation.HeaderCarrier(out.Header))

	pw := newProxyWriter(w)
	h.rp.ServeHTTP(pw, out)

	st.status = pw.status()
	st.bytesOut = pw.bytes
	if st.requestTap != nil {
		st.bytesIn = st.requestTap.Bytes()
	}
	h.mtr.ObserveBytes(agent, "in", st.bytesIn)
	h.mtr.ObserveBytes(agent, "out", pw.bytes)
	h.mtr.ObserveRequest(agent, string(binding), time.Since(start).Seconds(), pw.status())
}

// finishHandoff extracts A2A IDs from the bounded taps, records TR-3
// attributes and ends the span. It runs even when forwarding failed before
// any upstream response existed.
func (h *Handler) finishHandoff(st *handoffState) {
	if st == nil || st.span == nil {
		return
	}

	var reqPrefix, respPrefix []byte
	if st.requestTap != nil {
		reqPrefix = st.requestTap.Prefix()
		st.bytesIn = st.requestTap.Bytes()
	}
	if st.responseTap != nil {
		respPrefix = st.responseTap.Prefix()
	}
	ids := telemetry.ExtractPayloadIDs(reqPrefix, respPrefix)
	usage := telemetry.ExtractUsage(reqPrefix, respPrefix)

	attrs := []attribute.KeyValue{
		attribute.Int64(telemetry.AttrBytesIn, st.bytesIn),
		attribute.Int64(telemetry.AttrBytesOut, st.bytesOut),
		attribute.Float64(telemetry.AttrLatencyMS, float64(time.Since(st.start).Microseconds())/1000),
	}
	if st.status != 0 {
		attrs = append(attrs, attribute.Int(telemetry.AttrHTTPStatusCode, st.status))
	}
	if ids.TaskID != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrTaskID, ids.TaskID))
	}
	if ids.ContextID != "" {
		attrs = append(attrs,
			attribute.String(telemetry.AttrContextID, ids.ContextID),
			attribute.String(telemetry.AttrConversationID, ids.ContextID),
		)
	}
	if ids.TaskStatus != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrTaskStatus, ids.TaskStatus))
	}
	// Token usage and cost (CO-1, CO-2). Usage the agent did not report
	// stays unknown: the span says so instead of claiming zero tokens.
	if usage.Known {
		attrs = append(attrs,
			attribute.Int64(telemetry.AttrUsageInput, usage.InputTokens),
			attribute.Int64(telemetry.AttrUsageOutput, usage.OutputTokens),
			attribute.Bool(telemetry.AttrTokensKnown, true),
		)
	}
	if usage.Model != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrRequestModel, usage.Model))
	}
	if usage.Known && usage.Model != "" {
		if usd, ok := h.prices.Cost(usage.Model, usage.InputTokens, usage.OutputTokens); ok {
			attrs = append(attrs, attribute.Float64(telemetry.AttrCostUSD, usd))
		}
	}
	st.span.SetAttributes(attrs...)

	switch {
	case st.errorType != "":
		st.span.SetAttributes(attribute.String(telemetry.AttrErrorType, st.errorType))
		st.span.SetStatus(codes.Error, st.errorType)
		if st.err != nil {
			st.span.RecordError(st.err)
		}
	case st.status >= 500:
		st.span.SetAttributes(attribute.String(telemetry.AttrErrorType, "http_5xx"))
		st.span.SetStatus(codes.Error, "http_5xx")
	case ids.TaskStatus != "" && telemetry.TaskFailed(ids.TaskStatus):
		st.span.SetAttributes(attribute.String(telemetry.AttrErrorType, "task_failed"))
		st.span.SetStatus(codes.Error, "task_failed")
	}

	st.span.End()
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
	st := handoffStateFromContext(resp.Request.Context())
	if st != nil {
		st.status = resp.StatusCode
		if resp.Body != nil {
			// Bounded response prefix for task/context/status extraction;
			// no whole-body buffering, SSE included.
			tap := telemetry.NewBodyTap(resp.Body, telemetry.MaxTapBytes)
			resp.Body = tap
			st.responseTap = tap
		}
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil
	}
	agent := agentFromContext(resp.Request.Context())
	// The cap token is acquired before the gauge: a rejection can touch
	// neither a semaphore slot it never releases nor an active-stream
	// gauge entry that never decrements (both leak otherwise).
	if h.streamCap > 0 {
		select {
		case h.streams <- struct{}{}:
		default:
			h.mtr.StreamCapRejectionsTotal.WithLabelValues(agent).Inc()
			return &proxyError{
				status: http.StatusServiceUnavailable,
				reason: "stream_cap_exhausted",
				err:    fmt.Errorf("live SSE responses exceeded limits.max_concurrent_streams (%d)", h.streamCap),
			}
		}
	}
	// Reached only with the token (when capped) held; release below is the
	// single owner of both, so the {agent} gauge cannot over-count.
	h.mtr.ActiveStreams.WithLabelValues(agent).Inc()
	resp.Body = &streamTrackedBody{
		ReadCloser: resp.Body,
		release: func() {
			if h.streamCap > 0 {
				<-h.streams
			}
			h.mtr.ActiveStreams.WithLabelValues(agent).Dec()
		},
	}
	return nil
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
	st := handoffStateFromContext(r.Context())
	var typedErr *proxyError
	if errors.As(proxyErr, &typedErr) {
		if st != nil {
			st.fail(typedErr.status, typedErr.reason, typedErr.err)
		}
		h.logger.Debug("proxy request rejected",
			"agent", agentFromContext(r.Context()),
			"reason", typedErr.reason, "err", typedErr.err)
		h.writeJSONError(w, typedErr.status, typedErr.reason, typedErr.err.Error())
		return
	}

	reason := "upstream_error"
	status := http.StatusBadGateway
	switch {
	case errors.Is(proxyErr, context.DeadlineExceeded):
		reason = "upstream_timeout"
	case errors.Is(proxyErr, context.Canceled):
		reason = "client_canceled"
	case strings.Contains(proxyErr.Error(), "request body too large"):
		reason = "body_too_large"
		status = http.StatusRequestEntityTooLarge
		if st != nil {
			st.fail(status, reason, proxyErr)
		}
		h.logger.Debug("proxy request rejected", "agent", agentFromContext(r.Context()), "reason", reason)
		h.writeJSONError(w, status, reason,
			"proxied request body exceeds limits.max_request_body_bytes")
		return
	}
	if st != nil {
		st.fail(status, reason, proxyErr)
	}
	// Never log payloads: error text already excludes body bytes.
	h.logger.Debug("proxy error",
		"agent", agentFromContext(r.Context()), "reason", reason, "err", proxyErr)
	h.writeJSONError(w, status, reason,
		"upstream agent is unreachable or failed before producing a response")
}
