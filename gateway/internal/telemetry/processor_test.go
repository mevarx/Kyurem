package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// testRecorder is a concurrency-safe Recorder for pipeline assertions.
type testRecorder struct {
	mu        sync.Mutex
	ended     int
	exported  int
	failed    int
	retries   int
	dropped   map[string]int
	durations int
}

func newTestRecorder() *testRecorder {
	return &testRecorder{dropped: map[string]int{}}
}

func (r *testRecorder) SpanEnded() {
	r.mu.Lock()
	r.ended++
	r.mu.Unlock()
}

func (r *testRecorder) SpanDropped(reason string, n int) {
	r.mu.Lock()
	r.dropped[reason] += n
	r.mu.Unlock()
}

func (r *testRecorder) SpanExported(n int) {
	r.mu.Lock()
	r.exported += n
	r.mu.Unlock()
}

func (r *testRecorder) ExportFailed(int) {
	r.mu.Lock()
	r.failed++
	r.mu.Unlock()
}

func (r *testRecorder) ExportRetried() {
	r.mu.Lock()
	r.retries++
	r.mu.Unlock()
}

func (r *testRecorder) ObserveExportDuration(float64) {
	r.mu.Lock()
	r.durations++
	r.mu.Unlock()
}

func (r *testRecorder) SetQueueDepth(int, int) {}

func (r *testRecorder) snapshot() (ended, exported, failed, retries int, dropped map[string]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := make(map[string]int, len(r.dropped))
	for k, v := range r.dropped {
		copied[k] = v
	}
	return r.ended, r.exported, r.failed, r.retries, copied
}

// gateExporter gives the test deterministic control over export calls.
type gateExporter struct {
	mu        sync.Mutex
	batches   int
	spans     int
	failFirst int
	attempts  int
	started   chan struct{}
	release   chan struct{}
}

func (e *gateExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	e.attempts++
	attempt := e.attempts
	e.mu.Unlock()

	if attempt == 1 && e.started != nil {
		close(e.started)
		select {
		case <-e.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if attempt <= e.failFirst {
		return errors.New("collector unavailable")
	}

	e.mu.Lock()
	e.batches++
	e.spans += len(spans)
	e.mu.Unlock()
	return nil
}

func (e *gateExporter) Shutdown(context.Context) error { return nil }

func (e *gateExporter) counts() (batches, spans int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.batches, e.spans
}

func testSpan(name string) sdktrace.ReadOnlySpan {
	return tracetest.SpanStub{Name: name}.Snapshot()
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TR-5: a full queue drops spans immediately instead of blocking the
// request goroutine, and every dropped span is counted by reason.
func TestProcessorQueueOverflowDropsWithoutBlocking(t *testing.T) {
	exp := &gateExporter{started: make(chan struct{}), release: make(chan struct{})}
	rec := newTestRecorder()
	p := newAsyncProcessor(exp, rec, testLogger(), 2, 1, time.Hour, time.Second, 0, time.Millisecond, time.Millisecond)
	t.Cleanup(func() {
		_ = p.Shutdown(context.Background())
	})

	p.OnEnd(testSpan("first")) // consumer picks this up and blocks in the exporter
	select {
	case <-exp.started:
	case <-time.After(5 * time.Second):
		t.Fatal("exporter never started")
	}

	p.OnEnd(testSpan("second"))
	p.OnEnd(testSpan("third"))
	done := make(chan struct{})
	go func() {
		p.OnEnd(testSpan("overflow"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnEnd blocked on a full queue")
	}

	_, _, _, _, dropped := rec.snapshot()
	if dropped["queue_full"] != 1 {
		t.Fatalf("queue_full drops = %d, want 1 (%v)", dropped["queue_full"], dropped)
	}

	close(exp.release)
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	if _, spans := exp.counts(); spans != 3 {
		t.Fatalf("exported spans = %d, want 3", spans)
	}
}

// TR-5: failed exports retry with backoff until the batch succeeds.
func TestProcessorRetriesUntilSuccess(t *testing.T) {
	exp := &gateExporter{failFirst: 2}
	rec := newTestRecorder()
	p := newAsyncProcessor(exp, rec, testLogger(), 8, 1, time.Hour, time.Second, 3, time.Millisecond, 2*time.Millisecond)
	t.Cleanup(func() {
		_ = p.Shutdown(context.Background())
	})

	p.OnEnd(testSpan("retry-me"))
	waitFor(t, "retry success", func() bool {
		_, exported, _, _, _ := rec.snapshot()
		return exported == 1
	})

	ended, exported, failed, retries, dropped := rec.snapshot()
	if ended != 1 || exported != 1 || failed != 0 || retries != 2 || dropped["export_failed"] != 0 {
		t.Fatalf("ended=%d exported=%d failed=%d retries=%d dropped=%v", ended, exported, failed, retries, dropped)
	}
}

// TR-5: after the retry budget is exhausted the batch is dropped and both
// the failure and drop counters move.
func TestProcessorDropsAfterRetryBudget(t *testing.T) {
	exp := &gateExporter{failFirst: 100}
	rec := newTestRecorder()
	p := newAsyncProcessor(exp, rec, testLogger(), 8, 1, time.Hour, time.Second, 1, time.Millisecond, time.Millisecond)
	t.Cleanup(func() {
		_ = p.Shutdown(context.Background())
	})

	p.OnEnd(testSpan("doomed"))
	waitFor(t, "export failure accounting", func() bool {
		_, _, failed, _, _ := rec.snapshot()
		return failed == 1
	})

	_, _, failed, retries, dropped := rec.snapshot()
	if failed != 1 || retries != 1 || dropped["export_failed"] != 1 {
		t.Fatalf("failed=%d retries=%d dropped=%v", failed, retries, dropped)
	}
}

// ForceFlush exports a partial batch without waiting for the batch timer.
func TestProcessorForceFlushPartialBatch(t *testing.T) {
	exp := &gateExporter{}
	rec := newTestRecorder()
	p := newAsyncProcessor(exp, rec, testLogger(), 16, 8, time.Hour, time.Second, 0, time.Millisecond, time.Millisecond)
	t.Cleanup(func() {
		_ = p.Shutdown(context.Background())
	})

	p.OnEnd(testSpan("only-one"))
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	if _, spans := exp.counts(); spans != 1 {
		t.Fatalf("exported spans = %d, want 1", spans)
	}
}

// Shutdown drains everything already queued (including a partial batch) so
// a clean process exit does not lose spans.
func TestProcessorShutdownDrainsQueue(t *testing.T) {
	exp := &gateExporter{}
	rec := newTestRecorder()
	p := newAsyncProcessor(exp, rec, testLogger(), 16, 4, time.Hour, time.Second, 0, time.Millisecond, time.Millisecond)

	for i := 0; i < 7; i++ {
		p.OnEnd(testSpan("drain"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, spans := exp.counts(); spans != 7 {
		t.Fatalf("exported spans = %d, want 7", spans)
	}

	// Spans offered after shutdown are counted, never silently lost.
	p.OnEnd(testSpan("late"))
	_, _, _, _, dropped := rec.snapshot()
	if dropped["shutdown"] != 1 {
		t.Fatalf("shutdown drops = %d, want 1 (%v)", dropped["shutdown"], dropped)
	}
}
