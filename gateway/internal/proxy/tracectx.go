// Package proxy implements the A2A transparent forwarding core: routing,
// agent card rewriting, trace-context propagation and metrics-instrumented
// reverse proxying.
package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	// traceparentHeader is the W3C Trace Context header (GW-5).
	traceparentHeader = "traceparent"
	// tracestateHeader carries vendor trace state verbatim.
	tracestateHeader = "tracestate"
)

// deriveTraceparent takes an inbound `traceparent` header value and returns
// the value the gateway forwards upstream: same trace ID and flags, but a
// fresh span ID (the gateway hop spans under the caller's span, per W3C).
func deriveTraceparent(in string) (string, error) {
	parts := strings.Split(in, "-")
	if len(parts) != 4 {
		return "", fmt.Errorf("traceparent wants 4 fields, got %d", len(parts))
	}
	version, traceID, parentID, flags := parts[0], parts[1], parts[2], parts[3]
	if !isHex(version) || len(version) != 2 {
		return "", errors.New("traceparent version must be 2 hex chars")
	}
	if version == "ff" || version == "FF" {
		return "", errors.New("traceparent version ff is forbidden")
	}
	if len(traceID) != 32 || !isHex(traceID) {
		return "", errors.New("trace id must be 32 hex chars")
	}
	if len(parentID) != 16 || !isHex(parentID) {
		return "", errors.New("parent-to-span reference must be 16 hex raw")
	}
	if len(flags) != 2 || !isHex(flags) {
		return "", errors.New("flags must be 2 hex chars")
	}
	if allZeroHex(traceID) {
		return "", errors.New("trace id must not be all zero")
	}
	if allZeroHex(parentID) {
		return "", errors.New("span id must not be all zero")
	}
	spanID, err := newSpanID()
	if err != nil {
		return "", err
	}
	// Lower-case fields per W3C; flags copied verbatim (the caller's
	// sampling decision is preserved across the gateway hop).
	return fmt.Sprintf("00-%s-%s-%s", strings.ToLower(traceID), spanID, strings.ToLower(flags)), nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func allZeroHex(s string) bool {
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return true
}

// newSpanID returns a random 16-hex W3C span id that is never all zero
// (improbable as that is, W3C makes it undefined behavior).
func newSpanID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("span id entropy: %w", err)
	}
	if allZeroHex(hex.EncodeToString(b[:])) {
		b[0] |= 0x01
	}
	return hex.EncodeToString(b[:]), nil
}

// newTraceID returns a random 32-hex W3C trace id that is never all zero.
func newTraceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("trace id entropy: %w", err)
	}
	if allZeroHex(hex.EncodeToString(b[:])) {
		b[0] |= 0x01
	}
	return hex.EncodeToString(b[:]), nil
}

// newRootTraceparent builds a valid gateway-rooted trace context with the
// sampled (01) flag, used when the inbound context is missing or invalid.
// An empty string is returned only on entropy failure (never in practice).
func newRootTraceparent() string {
	span, err := newSpanID()
	if err != nil {
		return ""
	}
	trace, err := newTraceID()
	if err != nil {
		return ""
	}
	return fmt.Sprintf("00-%s-%s-01", trace, span)
}

// propagateTraceContext sets outbound W3C headers on the upstream request:
//
//   - valid inbound traceparent  -> derived child context (same trace id);
//   - absent or invalid inbound -> gateway-rooted fresh trace.
//
// Telemetry must never fail traffic (PRD §2.3), so all error paths degrade
// to a fresh trace instead of failing the request. When real spans arrive
// in M2, the derived value becomes the actual hop span's context.
func propagateTraceContext(outbound, inbound http.Header) {
	derived, err := deriveTraceparent(inbound.Get(traceparentHeader))
	if err != nil {
		derived = newRootTraceparent()
	}
	if derived != "" {
		outbound.Set(traceparentHeader, derived)
	}
	if ts := inbound.Get(tracestateHeader); ts != "" {
		outbound.Set(tracestateHeader, ts)
	}
}
