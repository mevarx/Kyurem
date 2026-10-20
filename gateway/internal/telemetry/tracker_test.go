package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
)

func spanContext(t *testing.T, traceIDHex, spanIDHex string) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex(traceIDHex)
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex(spanIDHex)
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}

func contextWithParent(t *testing.T, traceID, spanID string) context.Context {
	t.Helper()
	return trace.ContextWithRemoteSpanContext(context.Background(), spanContext(t, traceID, spanID))
}

func TestCallerTrackerAttributesNestedHop(t *testing.T) {
	tr := NewCallerTracker(4)
	tr.Remember(spanContext(t, testTraceID, testSpanID), "upper", 0)

	info := tr.Caller(contextWithParent(t, testTraceID, testSpanID), "")
	if info.Agent != "upper" || info.Hop != 1 {
		t.Fatalf("caller = %+v, want {upper 1}", info)
	}
}

func TestCallerTrackerFallsBackToSanitizedHint(t *testing.T) {
	tr := NewCallerTracker(4)
	info := tr.Caller(context.Background(), "  researcher\n")
	if info.Agent != "researcher" || info.Hop != 1 {
		t.Fatalf("caller = %+v, want {researcher 1}", info)
	}

	if got := tr.Caller(context.Background(), "bad\nname"); got.Agent != CallerUnknown {
		t.Fatalf("control characters must reject the hint, got %q", got.Agent)
	}
	if got := tr.Caller(context.Background(), ""); got.Agent != CallerUnknown || got.Hop != 0 {
		t.Fatalf("empty hint caller = %+v, want unknown/0", got)
	}
}

func TestCallerTrackerUnknownParentFallsThrough(t *testing.T) {
	tr := NewCallerTracker(4)
	info := tr.Caller(contextWithParent(t, testTraceID, testSpanID), "fallback")
	if info.Agent != "fallback" || info.Hop != 1 {
		t.Fatalf("caller = %+v, want fallback/1", info)
	}
}

// Capacity must bound memory: the oldest entry is evicted, later lookups
// degrade to the hint instead of growing the map.
func TestCallerTrackerEvictsOldest(t *testing.T) {
	tr := NewCallerTracker(2)
	first := spanContext(t, testTraceID, "0000000000000001")
	second := spanContext(t, testTraceID, "0000000000000002")
	third := spanContext(t, testTraceID, "0000000000000003")

	tr.Remember(first, "one", 0)
	tr.Remember(second, "two", 0)
	tr.Remember(third, "three", 0)

	ctxFirst := trace.ContextWithRemoteSpanContext(context.Background(), first)
	if got := tr.Caller(ctxFirst, "hint"); got.Agent != "hint" {
		t.Fatalf("evicted entry = %q, want hint fallback", got.Agent)
	}
	ctxThird := trace.ContextWithRemoteSpanContext(context.Background(), third)
	if got := tr.Caller(ctxThird, ""); got.Agent != "three" {
		t.Fatalf("newest entry = %q, want three", got.Agent)
	}
}

func TestCallerTrackerIgnoresInvalidContext(t *testing.T) {
	tr := NewCallerTracker(2)
	tr.Remember(trace.SpanContext{}, "nobody", 0) // invalid: no entry
	if got := tr.Caller(context.Background(), "hint"); got.Agent != "hint" {
		t.Fatalf("caller = %q, want hint", got.Agent)
	}
}
