package telemetry

import (
	"context"
	"strings"
	"sync"
	"unicode"

	"go.opentelemetry.io/otel/trace"
)

// CallerInfo records who invoked a gateway span. It is inferred from the
// remote parent span when a nested agent propagates trace context, or from
// the optional X-Kyurem-Caller hint otherwise.
type CallerInfo struct {
	Agent string
	Hop   int
}

type callerKey struct {
	traceID trace.TraceID
	spanID  trace.SpanID
}

// CallerTracker remembers spans emitted by this process so a nested
// handoff can attribute its caller without changing agent code. The map is
// bounded by a ring of keys: memory is O(capacity) under any workload and
// no request path ever blocks on eviction.
type CallerTracker struct {
	mu       sync.Mutex
	entries  map[callerKey]CallerInfo
	ring     []callerKey
	next     int
	capacity int
}

// NewCallerTracker builds a bounded tracker. A non-positive capacity uses
// a conservative default.
func NewCallerTracker(capacity int) *CallerTracker {
	if capacity <= 0 {
		capacity = 4096
	}
	return &CallerTracker{
		entries:  make(map[callerKey]CallerInfo, capacity),
		ring:     make([]callerKey, capacity),
		capacity: capacity,
	}
}

// Remember associates a span this gateway emitted with its callee and hop.
// It is called at span start, so a nested request made while the span is
// still running can find its parent.
func (t *CallerTracker) Remember(sc trace.SpanContext, agent string, hop int) {
	if t == nil || !sc.IsValid() {
		return
	}
	key := callerKey{traceID: sc.TraceID(), spanID: sc.SpanID()}

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.entries[key]; !exists {
		if len(t.ring) > 0 {
			old := t.ring[t.next]
			delete(t.entries, old)
			t.ring[t.next] = key
			t.next = (t.next + 1) % len(t.ring)
		}
	}
	t.entries[key] = CallerInfo{Agent: agent, Hop: hop}
}

// Caller resolves the caller for an extracted trace context. Precedence:
// the tracked remote parent (trustworthy within this process), then the
// optional hint header, then "unknown". The hint is untrusted attribution
// only and is sanitized to a bounded, log-safe token.
func (t *CallerTracker) Caller(ctx context.Context, hint string) CallerInfo {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() && t != nil {
		key := callerKey{traceID: sc.TraceID(), spanID: sc.SpanID()}
		t.mu.Lock()
		info, ok := t.entries[key]
		t.mu.Unlock()
		if ok {
			return CallerInfo{Agent: info.Agent, Hop: info.Hop + 1}
		}
	}
	if agent := sanitizeAgent(hint); agent != "" {
		return CallerInfo{Agent: agent, Hop: 1}
	}
	return CallerInfo{Agent: CallerUnknown, Hop: 0}
}

// sanitizeAgent bounds an untrusted header to a short, printable token.
func sanitizeAgent(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	runes := []rune(v)
	if len(runes) > 128 {
		runes = runes[:128]
	}
	for _, r := range runes {
		if !unicode.IsPrint(r) {
			return ""
		}
	}
	return string(runes)
}
