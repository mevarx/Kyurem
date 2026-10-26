package telemetry

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A full queue must not log once per span: a burst yields one aggregated
// warning, and the drop count is carried rather than lost.
func TestSinkProcessorRateLimitsDropWarnings(t *testing.T) {
	var buf syncBuffer
	p := &sinkProcessor{
		logger: slog.New(slog.NewTextHandler(&buf, nil)),
		queue:  make(chan sdktrace.ReadOnlySpan), // unbuffered, never drained: every span drops
	}
	span := tracetest.SpanStub{}.Snapshot()
	const burst = 500
	for i := 0; i < burst; i++ {
		p.OnEnd(span)
	}
	if n := strings.Count(buf.String(), "queue full"); n != 1 {
		t.Fatalf("got %d drop warnings for a %d-span burst, want 1:\n%s", n, burst, buf.String())
	}
	if got := p.dropped.Load(); got != burst-1 {
		t.Fatalf("pending dropped = %d, want %d (first drop was reported)", got, burst-1)
	}
}
