package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func validTraceparent(traceID, spanID string) string {
	return "00-" + traceID + "-" + spanID + "-01"
}

func TestDeriveTraceparentValidInbound(t *testing.T) {
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	parent := "00f067aa0ba902b7"
	out, err := deriveTraceparent(validTraceparent(trace, parent))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	parts := strings.Split(out, "-")
	if len(parts) != 4 {
		t.Fatalf("derived traceparent shape: %q", out)
	}
	if parts[1] != trace {
		t.Errorf("trace id must be preserved, got %s want %s", parts[1], trace)
	}
	if parts[2] == parent {
		t.Errorf("span id must change per hop, got %s", parts[2])
	}
	if len(parts[2]) != 16 || allZeroHex(parts[2]) {
		t.Errorf("derived span id invalid: %s", parts[2])
	}
	if parts[3] != "01" {
		t.Errorf("flags must be copied verbatim, got %s", parts[3])
	}
}

func TestDeriveTraceparentRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"space in trace":  "00-4bf92f3577b34da6a3ce929d0e0e473 -00f067aa0ba902b7-01",
		"all zero trace":  "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"all zero parent": "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"version ff":      "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"short trace":     "00-4bf9-00f067aa0ba902b7-01",
		"non hex":         "00-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz-00f067aa0ba902b7-01",
		"no flags":        "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
		"extra dash":      "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-1",
		"empty":           "",
	}
	for name, in := range cases {
		if _, err := deriveTraceparent(in); err == nil {
			t.Errorf("%s: expected error, got success", name)
		}
	}
}

func TestPropagateAlwaysSetsOutgoing(t *testing.T) {
	outbound := http.Header{}
	propagateTraceContext(outbound, http.Header{})
	parent := outbound.Get(traceparentHeader)
	if parent == "" {
		t.Fatal("gateway must always send a traceparent upstream (ADR 0003)")
	}
	if parts := strings.Split(parent, "-"); len(parts) != 4 {
		t.Fatalf("fresh traceparent malformed: %q", parent)
	}
}

func TestPropagateInvalidInboundReplaced(t *testing.T) {
	outbound := http.Header{}
	inbound := http.Header{}
	inbound.Set(traceparentHeader, "not-a-traceparent")
	propagateTraceContext(outbound, inbound)
	got := outbound.Get(traceparentHeader)
	if got == "" || got == "not-a-traceparent" {
		t.Errorf("invalid inbound must be replaced by a fresh trace, got %q", got)
	}
}

func TestPropagateTracestateCopied(t *testing.T) {
	outbound := http.Header{}
	inbound := http.Header{}
	inbound.Set(traceparentHeader, validTraceparent(
		"4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7"))
	inbound.Set(tracestateHeader, "vendor=1")
	propagateTraceContext(outbound, inbound)
	if outbound.Get(tracestateHeader) != "vendor=1" {
		t.Errorf("tracestate must be carried verbatim, got %q", outbound.Get(tracestateHeader))
	}
}
