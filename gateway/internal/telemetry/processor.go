package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Recorder receives processor accounting. Implementations must be
// non-blocking; the hot path calls them from OnEnd. All methods are
// nil-safe through NopRecorder.
type Recorder interface {
	SpanEnded()
	SpanDropped(reason string, n int)
	SpanExported(n int)
	ExportFailed(n int)
	ExportRetried()
	ObserveExportDuration(seconds float64)
	SetQueueDepth(depth, capacity int)
}

// NopRecorder discards accounting; useful for tests and disabled exports.
type NopRecorder struct{}

func (NopRecorder) SpanEnded()                    {}
func (NopRecorder) SpanDropped(string, int)       {}
func (NopRecorder) SpanExported(int)              {}
func (NopRecorder) ExportFailed(int)              {}
func (NopRecorder) ExportRetried()                {}
func (NopRecorder) ObserveExportDuration(float64) {}
func (NopRecorder) SetQueueDepth(int, int)        {}

// asyncProcessor batches ended spans and exports them off the request
// path. Unlike the SDK BatchSpanProcessor it never blocks OnEnd and it
// accounts for every dropped span (TR-5): queue overflow, shutdown and
// exhausted retries are all counted before a span is discarded.
type asyncProcessor struct {
	exporter      sdktrace.SpanExporter
	rec           Recorder
	logger        *slog.Logger
	queue         chan sdktrace.ReadOnlySpan
	batchSize     int
	batchTimeout  time.Duration
	exportTimeout time.Duration
	maxRetries    int
	initialDelay  time.Duration
	maxDelay      time.Duration

	mu      sync.RWMutex
	closed  bool
	stop    chan struct{}
	done    chan struct{}
	flushCh chan chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
}

var _ sdktrace.SpanProcessor = (*asyncProcessor)(nil)

func newAsyncProcessor(
	exporter sdktrace.SpanExporter,
	rec Recorder,
	logger *slog.Logger,
	queueSize, batchSize int,
	batchTimeout, exportTimeout time.Duration,
	maxRetries int,
	initialDelay, maxDelay time.Duration,
) *asyncProcessor {
	if rec == nil {
		rec = NopRecorder{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &asyncProcessor{
		exporter:      exporter,
		rec:           rec,
		logger:        logger,
		queue:         make(chan sdktrace.ReadOnlySpan, queueSize),
		batchSize:     batchSize,
		batchTimeout:  batchTimeout,
		exportTimeout: exportTimeout,
		maxRetries:    maxRetries,
		initialDelay:  initialDelay,
		maxDelay:      maxDelay,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		flushCh:       make(chan chan struct{}, 1),
		ctx:           ctx,
		cancel:        cancel,
	}
	p.rec.SetQueueDepth(0, queueSize)
	go p.run()
	return p
}

// OnStart is a no-op: handoff spans are fully described at start and
// enriched before End.
func (p *asyncProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

// OnEnd enqueues without ever blocking. A full queue drops the span and
// increments the drop counter instead of stalling the request goroutine.
func (p *asyncProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	p.rec.SpanEnded()

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		p.rec.SpanDropped("shutdown", 1)
		return
	}
	select {
	case p.queue <- span:
		p.rec.SetQueueDepth(len(p.queue), cap(p.queue))
	default:
		p.rec.SpanDropped("queue_full", 1)
	}
}

// Shutdown stops accepting spans, drains the queue and waits for in-flight
// exports. On deadline the processor cancels outstanding exports, drops the
// remaining batch and returns the context error.
func (p *asyncProcessor) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.stop)
	}
	p.mu.Unlock()

	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		p.cancel()
		<-p.done
		return ctx.Err()
	}
}

// ForceFlush exports everything enqueued before the call and waits for the
// batches to finish (bounded by ctx; export attempts have export_timeout).
func (p *asyncProcessor) ForceFlush(ctx context.Context) error {
	p.mu.RLock()
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		select {
		case <-p.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	req := make(chan struct{})
	select {
	case p.flushCh <- req:
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-req:
		return nil
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *asyncProcessor) run() {
	defer close(p.done)
	defer p.cancel()

	batch := make([]sdktrace.ReadOnlySpan, 0, p.batchSize)
	timer := time.NewTimer(p.batchTimeout)
	defer timer.Stop()
	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(p.batchTimeout)
	}

	for {
		select {
		case span := <-p.queue:
			batch = append(batch, span)
			if len(batch) >= p.batchSize {
				p.exportBatch(batch)
				batch = batch[:0]
				p.rec.SetQueueDepth(len(p.queue), cap(p.queue))
				resetTimer()
			}
		case <-timer.C:
			if len(batch) > 0 {
				p.exportBatch(batch)
				batch = batch[:0]
				p.rec.SetQueueDepth(len(p.queue), cap(p.queue))
			}
			timer.Reset(p.batchTimeout)
		case req := <-p.flushCh:
			batch = p.flushAll(batch)
			p.rec.SetQueueDepth(len(p.queue), cap(p.queue))
			resetTimer()
			close(req)
		case <-p.stop:
			// Shutdown holds the write lock while closing stop, so no
			// OnEnd call can enqueue after this point.
			batch = p.flushAll(batch)
			p.rec.SetQueueDepth(0, cap(p.queue))
			return
		}
	}
}

// flushAll exports the current partial batch and then drains the queue in
// full batches until it is empty. It returns an empty, reusable batch.
func (p *asyncProcessor) flushAll(batch []sdktrace.ReadOnlySpan) []sdktrace.ReadOnlySpan {
	for {
		if len(batch) > 0 {
			p.exportBatch(batch)
			batch = batch[:0]
		}
		for len(batch) < p.batchSize {
			select {
			case span := <-p.queue:
				batch = append(batch, span)
			default:
				if len(batch) > 0 {
					p.exportBatch(batch)
					batch = batch[:0]
				}
				return batch
			}
		}
	}
}

func (p *asyncProcessor) exportBatch(spans []sdktrace.ReadOnlySpan) {
	n := len(spans)
	delay := p.initialDelay
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(p.ctx, p.exportTimeout)
		start := time.Now()
		err := p.exporter.ExportSpans(ctx, spans)
		cancel()
		p.rec.ObserveExportDuration(time.Since(start).Seconds())

		if err == nil {
			p.rec.SpanExported(n)
			return
		}
		if p.ctx.Err() != nil || errors.Is(err, context.Canceled) {
			p.rec.SpanDropped("shutdown", n)
			return
		}
		if attempt >= p.maxRetries {
			p.rec.ExportFailed(n)
			p.rec.SpanDropped("export_failed", n)
			p.logger.Warn("telemetry export failed; spans dropped",
				"spans", n, "attempts", attempt+1, "err", err)
			return
		}

		p.rec.ExportRetried()
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-p.ctx.Done():
			timer.Stop()
			p.rec.SpanDropped("shutdown", n)
			return
		}
		if delay < p.maxDelay {
			delay *= 2
			if delay > p.maxDelay {
				delay = p.maxDelay
			}
		}
	}
}
