package bench

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func adminWithGauge(t *testing.T, gauge func(call int64) int) *httptest.Server {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		fmt.Fprintf(w, "go_goroutines 10\nkyurem_proxy_active_streams{agent=\"echo\"} %d\n", gauge(n))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Streams still tearing down after the last worker stops must be waited
// for, and the pre-drain snapshot kept so the lag stays visible.
func TestAwaitDrainWaitsForActiveStreamsToReachZero(t *testing.T) {
	srv := adminWithGauge(t, func(n int64) int { return max(0, 3-int(n-1)) })
	obs := &GatewayObservation{}
	awaitDrain(context.Background(), &Spec{AdminURL: srv.URL, DrainTimeout: 10 * time.Second}, obs)
	if !obs.Drained || obs.After.ActiveStreams != 0 {
		t.Fatalf("drained=%v after=%v, want drained with 0 active", obs.Drained, obs.After.ActiveStreams)
	}
	if obs.AfterRun.ActiveStreams != 3 {
		t.Fatalf("after-run active = %v, want 3", obs.AfterRun.ActiveStreams)
	}
}

// A leaked stream must surface as not drained instead of passing silently.
func TestAwaitDrainReportsLeakedStreams(t *testing.T) {
	srv := adminWithGauge(t, func(int64) int { return 2 })
	obs := &GatewayObservation{}
	awaitDrain(context.Background(), &Spec{AdminURL: srv.URL, DrainTimeout: time.Second}, obs)
	if obs.Drained || obs.After.ActiveStreams != 2 {
		t.Fatalf("drained=%v after=%v, want not drained with 2 active", obs.Drained, obs.After.ActiveStreams)
	}
}

// An unreachable admin endpoint is a failed observation, never a clean drain.
func TestAwaitDrainUnreachableAdminIsNotDrained(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	obs := &GatewayObservation{}
	awaitDrain(context.Background(), &Spec{AdminURL: srv.URL, DrainTimeout: time.Second}, obs)
	if obs.Drained || obs.ScrapeFailed == "" {
		t.Fatalf("drained=%v scrape_failed=%q, want not drained with scrape error", obs.Drained, obs.ScrapeFailed)
	}
}
