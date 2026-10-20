package storage

import (
	"context"
	"sort"
	"sync"

	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

// DefaultMemoryCapacity is the default number of spans kept in memory.
const DefaultMemoryCapacity = 4096

// MemoryStore is a bounded, in-process span store.
//
// It is the driver that makes the management API useful without any
// external database: the gateway feeds finished spans into it through
// the telemetry.Sink interface and the dashboard reads them back through
// the Store interface. It is also the fastest way to see data during
// local development.
//
// The store is a ring buffer: at most capacity spans are held and the
// oldest one is dropped to make room for a new one. It never blocks and
// never grows without bound, because the telemetry pipeline calls
// AcceptSpan on the request path and a slow sink would slow the A2A data
// path. Spans that fall out of the ring are simply gone; use the
// ClickHouse driver when spans must survive the process.
//
// The zero value is usable: it behaves like a store with the default
// capacity. NewMemoryStore is the explicit way to build one.
type MemoryStore struct {
	mu       sync.Mutex
	buf      []telemetry.SpanRecord
	capacity int
	// next is the slot the next accepted span is written to. When the
	// buffer is full it also points at the oldest span, which is the one
	// that gets evicted.
	next int
	// count is the number of spans currently held.
	count int
}

// Compile-time assertions: MemoryStore is both a telemetry sink (it
// receives finished spans from the exporter) and a Store (the management
// API reads spans back out of it).
var (
	_ telemetry.Sink = (*MemoryStore)(nil)
	_ Store          = (*MemoryStore)(nil)
)

// NewMemoryStore builds an in-memory store keeping at most capacity
// spans. A non-positive capacity means DefaultMemoryCapacity.
func NewMemoryStore(capacity int) *MemoryStore {
	if capacity <= 0 {
		capacity = DefaultMemoryCapacity
	}
	return &MemoryStore{
		buf:      make([]telemetry.SpanRecord, capacity),
		capacity: capacity,
	}
}

// AcceptSpan stores a finished span, evicting the oldest one when the
// ring is full.
//
// The record's attribute maps are copied: the caller owns the record it
// hands over and may reuse or mutate it afterwards, which must not be
// able to corrupt what the dashboard reads.
func (s *MemoryStore) AcceptSpan(rec telemetry.SpanRecord) {
	if s == nil {
		return
	}
	stored := memoryCloneRecord(rec)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.buf[s.next] = stored
	s.next = (s.next + 1) % s.capacity
	if s.count < s.capacity {
		s.count++
	}
}

// Spans returns the spans matching the query, oldest first.
//
// A span is included when it falls inside the query window
// ([From, To), a zero bound meaning unbounded) and, when the query names
// a trace, when it belongs to that trace. The result is sorted by start
// time and truncated to the query limit when the limit is positive.
//
// The returned records are copies: callers cannot reach into the store
// through them.
func (s *MemoryStore) Spans(ctx context.Context, q SpanQuery) ([]telemetry.SpanRecord, error) {
	if s == nil {
		return []telemetry.SpanRecord{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.initLocked()
	out := make([]telemetry.SpanRecord, 0, s.count)
	head := s.head()
	for i := 0; i < s.count; i++ {
		rec := s.buf[(head+i)%s.capacity]
		if !memoryInWindow(rec, q.Window) {
			continue
		}
		if q.TraceID != "" && rec.TraceID != q.TraceID {
			continue
		}
		out = append(out, memoryCloneRecord(rec))
	}
	s.mu.Unlock()

	// The ring keeps insertion order, not start-time order: a span that
	// started earlier can finish later. Sort before returning, keeping
	// insertion order for spans that share a start time.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// Close implements Store. The store holds no resources, so it is a no-op.
func (s *MemoryStore) Close() error {
	return nil
}

// Len returns the number of spans currently held by the store.
func (s *MemoryStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// initLocked makes sure the ring is usable. It is called on every
// mutating and reading path so that a MemoryStore built as a zero value
// behaves like a store with the default capacity instead of panicking.
// The caller must hold the mutex.
func (s *MemoryStore) initLocked() {
	if s.capacity > 0 && len(s.buf) == s.capacity {
		return
	}
	capacity := s.capacity
	if capacity <= 0 {
		capacity = DefaultMemoryCapacity
	}
	s.capacity = capacity
	s.buf = make([]telemetry.SpanRecord, capacity)
	s.next = 0
	s.count = 0
}

// head returns the index of the oldest span in the ring. The caller must
// hold the mutex.
func (s *MemoryStore) head() int {
	if s.count == 0 {
		return 0
	}
	return (s.next - s.count + s.capacity) % s.capacity
}

// memoryInWindow reports whether a span falls inside the query window. A zero
// From means no lower bound, a zero To means no upper bound.
func memoryInWindow(rec telemetry.SpanRecord, w Window) bool {
	if !w.From.IsZero() && rec.StartedAt.Before(w.From) {
		return false
	}
	if !w.To.IsZero() && !rec.StartedAt.Before(w.To) {
		return false
	}
	return true
}

// memoryCloneRecord copies a span record and its attribute maps.
func memoryCloneRecord(rec telemetry.SpanRecord) telemetry.SpanRecord {
	out := rec
	out.Attributes = memoryCloneAttrs(rec.Attributes)
	out.ResourceAttributes = memoryCloneAttrs(rec.ResourceAttributes)
	return out
}

// memoryCloneAttrs copies an attribute map. A nil map stays nil.
func memoryCloneAttrs(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
