package telemetry

import (
	"context"
	"log/slog"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// sinkProcessor forwards finished spans to a management-API sink.
//
// It is deliberately independent of the OTLP exporter: the in-process span
// store must keep working when export is disabled (no collector, local
// development, tests). Conversion to the normalized record happens on the
// drain goroutine, not on the request path, and a full queue drops the
// record instead of blocking the data path.
type sinkProcessor struct {
	sink   Sink
	logger *slog.Logger

	queue  chan sdktrace.ReadOnlySpan
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	closed bool
	mu     sync.RWMutex
}

var _ sdktrace.SpanProcessor = (*sinkProcessor)(nil)

// newSinkProcessor starts the drain goroutine. A nil sink yields nil.
func newSinkProcessor(sink Sink, logger *slog.Logger) *sinkProcessor {
	if sink == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	p := &sinkProcessor{
		sink:   sink,
		logger: logger,
		queue:  make(chan sdktrace.ReadOnlySpan, 1024),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go p.run()
	return p
}

// OnStart is a no-op: handoff spans are fully described at start.
func (p *sinkProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

// OnEnd enqueues the span without blocking. Overflow drops the record:
// the sink is a best-effort view, never a reason to slow the data path.
func (p *sinkProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return
	}
	select {
	case p.queue <- span:
	default:
		p.logger.Warn("telemetry sink queue full; span not published to the management store")
	}
}

// Shutdown stops accepting spans and drains what is queued.
func (p *sinkProcessor) Shutdown(ctx context.Context) error {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		close(p.stop)
	})
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ForceFlush drains the queue.
func (p *sinkProcessor) ForceFlush(ctx context.Context) error {
	for {
		p.mu.RLock()
		queued := len(p.queue)
		p.mu.RUnlock()
		if queued == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (p *sinkProcessor) run() {
	defer close(p.done)
	for {
		select {
		case span := <-p.queue:
			p.sink.AcceptSpan(RecordFromSpan(span))
		case <-p.stop:
			// Drain what is left, then stop.
			for {
				select {
				case span := <-p.queue:
					p.sink.AcceptSpan(RecordFromSpan(span))
				default:
					return
				}
			}
		}
	}
}
