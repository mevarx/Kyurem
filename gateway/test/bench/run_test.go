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

// lateWriter records writes that happen after Run returned. It models the
// CLI's stderr writer: still valid, but a background progress print after
// the result is published is a lifecycle bug (and races with callers).
type lateWriter struct {
	after  atomic.Bool
	writes atomic.Int64
}

func (w *lateWriter) Write(p []byte) (int, error) {
	if w.after.Load() {
		w.writes.Add(1)
	}
	return len(p), nil
}

// TestRunJoinsBackgroundGoroutines pins the driver's lifecycle: the
// active-streams poller and the progress printer are joined before Run
// returns, so Result.Gateway is settled and no goroutine writes to the
// caller's writer afterwards. Before the join, the poller woke at 500 ms
// and the progress printer at 1 s — both after a short run returned.
func TestRunJoinsBackgroundGoroutines(t *testing.T) {
	stack := startStack(t, 16)

	// The admin endpoint reports a rising gauge so a leaked poller would
	// publish a peak after the run; /version can answer with metrics too,
	// the driver treats an undecodable body as "no provenance".
	var scrapes atomic.Int64
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := scrapes.Add(1)
		fmt.Fprintf(w, "# TYPE kyurem_proxy_active_streams gauge\n"+
			"kyurem_proxy_active_streams %d\n", n)
	}))
	defer admin.Close()

	progress := &lateWriter{}
	res, err := Run(context.Background(), &Spec{
		Label:    "join",
		Routes:   []string{stack.route()},
		AdminURL: admin.URL,
		Streams:  1,
		Requests: 1,
		Timeout:  10 * time.Second,
		Progress: progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Completed != 1 {
		t.Fatalf("completed = %d, want 1 (%s)", res.Completed, res.String())
	}

	// From here on, any write to progress or change to the peak is a
	// write that escaped the run (the race detector also flags it when
	// these fields are accessed concurrently).
	peakAtReturn := res.Gateway.ActiveStreamsPeak
	progress.after.Store(true)
	time.Sleep(1200 * time.Millisecond)

	if n := progress.writes.Load(); n != 0 {
		t.Fatalf("%d progress writes landed after Run returned", n)
	}
	if got := res.Gateway.ActiveStreamsPeak; got != peakAtReturn {
		t.Fatalf("active-streams peak changed after Run returned: %v -> %v", peakAtReturn, got)
	}
}
