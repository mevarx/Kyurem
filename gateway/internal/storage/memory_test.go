package storage

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

// memBase is a fixed reference time so the tests never depend on the wall
// clock.
var memBase = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// memRec builds a span record with the given trace, span id, start
// offset and attributes.
func memRec(traceID, spanID string, offset time.Duration, attrs map[string]string) telemetry.SpanRecord {
	return telemetry.SpanRecord{
		TraceID:            traceID,
		SpanID:             spanID,
		ParentSpanID:       "parent-" + spanID,
		Name:               "span-" + spanID,
		Kind:               "server",
		ServiceName:        "kyurem-gateway",
		StartedAt:          memBase.Add(offset),
		EndedAt:            memBase.Add(offset + 25*time.Millisecond),
		StatusCode:         "Ok",
		StatusMessage:      "",
		Attributes:         attrs,
		ResourceAttributes: map[string]string{"service.name": "kyurem-gateway"},
	}
}

// memSpanIDs extracts the span ids of the given records, in order.
func memSpanIDs(recs []telemetry.SpanRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.SpanID)
	}
	return out
}

func memEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMemoryStoreSpansRoundTrip checks that accepted spans come back
// with their attributes intact, ordered by start time, and that the
// store owns its data: mutating the caller's map after AcceptSpan, or a
// returned map, must not change what the store holds.
func TestMemoryStoreSpansRoundTrip(t *testing.T) {
	s := NewMemoryStore(8)

	attrs := map[string]string{"kyurem.hop": "2", "gen_ai.model": "test-model"}
	s.AcceptSpan(memRec("trace-a", "span-c", 2*time.Second, attrs))
	s.AcceptSpan(memRec("trace-a", "span-a", 0, map[string]string{"kyurem.hop": "1"}))
	s.AcceptSpan(memRec("trace-b", "span-b", time.Second, map[string]string{"kyurem.hop": "1"}))

	// The caller keeps ownership of the map it handed over.
	attrs["kyurem.hop"] = "mutated"
	attrs["extra"] = "mutated"

	got, err := s.Spans(context.Background(), SpanQuery{})
	if err != nil {
		t.Fatalf("Spans() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Spans() returned %d spans, want 3", len(got))
	}
	if !memEqualStrings(memSpanIDs(got), []string{"span-a", "span-b", "span-c"}) {
		t.Fatalf("Spans() order = %v, want [span-a span-b span-c]", memSpanIDs(got))
	}
	if got[2].Attributes["kyurem.hop"] != "2" {
		t.Errorf("attribute kyurem.hop = %q, want %q", got[2].Attributes["kyurem.hop"], "2")
	}
	if _, ok := got[2].Attributes["extra"]; ok {
		t.Errorf("store kept a key the caller added after AcceptSpan")
	}
	if got[0].Attributes["kyurem.hop"] != "1" {
		t.Errorf("attribute kyurem.hop = %q, want %q", got[0].Attributes["kyurem.hop"], "1")
	}
	if got[0].ResourceAttributes["service.name"] != "kyurem-gateway" {
		t.Errorf("resource attribute service.name = %q, want %q", got[0].ResourceAttributes["service.name"], "kyurem-gateway")
	}
	if got[0].Name != "span-span-a" {
		t.Errorf("Name = %q, want %q", got[0].Name, "span-span-a")
	}

	// The caller owns what it got back.
	got[0].Attributes["kyurem.hop"] = "tampered"
	got[0].ResourceAttributes["service.name"] = "tampered"

	again, err := s.Spans(context.Background(), SpanQuery{})
	if err != nil {
		t.Fatalf("Spans() error = %v", err)
	}
	if again[0].Attributes["kyurem.hop"] != "1" {
		t.Errorf("attribute kyurem.hop = %q after mutating the returned map, want %q", again[0].Attributes["kyurem.hop"], "1")
	}
	if again[0].ResourceAttributes["service.name"] != "kyurem-gateway" {
		t.Errorf("resource attribute service.name = %q after mutating the returned map, want %q", again[0].ResourceAttributes["service.name"], "kyurem-gateway")
	}
}

// TestMemoryStoreRingDropsOldest checks that the store stays bounded and
// evicts the oldest span first.
func TestMemoryStoreRingDropsOldest(t *testing.T) {
	s := NewMemoryStore(3)
	for i := 0; i < 5; i++ {
		s.AcceptSpan(memRec("trace-a", fmt.Sprintf("span-%d", i), time.Duration(i)*time.Second, nil))
	}

	if got := s.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}
	got, err := s.Spans(context.Background(), SpanQuery{})
	if err != nil {
		t.Fatalf("Spans() error = %v", err)
	}
	want := []string{"span-2", "span-3", "span-4"}
	if !memEqualStrings(memSpanIDs(got), want) {
		t.Fatalf("Spans() = %v, want %v", memSpanIDs(got), want)
	}
}

// TestMemoryStoreDefaultCapacity checks that a non-positive capacity
// falls back to the default and that the store still works.
func TestMemoryStoreDefaultCapacity(t *testing.T) {
	s := NewMemoryStore(0)
	s.AcceptSpan(memRec("trace-a", "span-a", 0, nil))
	if got := s.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	if got, err := s.Spans(context.Background(), SpanQuery{}); err != nil || len(got) != 1 {
		t.Fatalf("Spans() = %d spans, err = %v, want 1 span and no error", len(got), err)
	}
}

// TestMemoryStoreFilters covers the window and trace-id filters.
func TestMemoryStoreFilters(t *testing.T) {
	s := NewMemoryStore(16)
	s.AcceptSpan(memRec("trace-a", "span-a", 0, nil))              // at memBase
	s.AcceptSpan(memRec("trace-a", "span-b", 10*time.Second, nil)) // at memBase+10s
	s.AcceptSpan(memRec("trace-b", "span-c", 20*time.Second, nil)) // at memBase+20s
	s.AcceptSpan(memRec("trace-b", "span-d", 30*time.Second, nil)) // at memBase+30s

	cases := []struct {
		name  string
		query SpanQuery
		want  []string
	}{
		{
			name:  "no filter returns everything oldest first",
			query: SpanQuery{},
			want:  []string{"span-a", "span-b", "span-c", "span-d"},
		},
		{
			name: "window excludes spans before from",
			query: SpanQuery{Window: Window{
				From: memBase.Add(10 * time.Second),
			}},
			want: []string{"span-b", "span-c", "span-d"},
		},
		{
			name: "window excludes spans at or after to",
			query: SpanQuery{Window: Window{
				To: memBase.Add(20 * time.Second),
			}},
			want: []string{"span-a", "span-b"},
		},
		{
			name: "bounded window",
			query: SpanQuery{Window: Window{
				From: memBase.Add(10 * time.Second),
				To:   memBase.Add(30 * time.Second),
			}},
			want: []string{"span-b", "span-c"},
		},
		{
			name: "zero to is unbounded",
			query: SpanQuery{Window: Window{
				From: memBase.Add(25 * time.Second),
				To:   time.Time{},
			}},
			want: []string{"span-d"},
		},
		{
			name:  "trace id filter",
			query: SpanQuery{TraceID: "trace-b"},
			want:  []string{"span-c", "span-d"},
		},
		{
			name: "trace id filter combined with window",
			query: SpanQuery{
				Window:  Window{From: memBase.Add(15 * time.Second)},
				TraceID: "trace-b",
			},
			want: []string{"span-c", "span-d"},
		},
		{
			name:  "unknown trace id matches nothing",
			query: SpanQuery{TraceID: "trace-zzz"},
			want:  []string{},
		},
		{
			name: "window matching nothing",
			query: SpanQuery{Window: Window{
				From: memBase.Add(time.Hour),
			}},
			want: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Spans(context.Background(), tc.query)
			if err != nil {
				t.Fatalf("Spans() error = %v", err)
			}
			if !memEqualStrings(memSpanIDs(got), tc.want) {
				t.Fatalf("Spans() = %v, want %v", memSpanIDs(got), tc.want)
			}
		})
	}
}

// TestMemoryStoreLimit checks that a positive limit truncates the sorted
// result and that a zero limit returns everything.
func TestMemoryStoreLimit(t *testing.T) {
	s := NewMemoryStore(16)
	for i := 0; i < 5; i++ {
		s.AcceptSpan(memRec("trace-a", fmt.Sprintf("span-%d", i), time.Duration(i)*time.Second, nil))
	}

	cases := []struct {
		name  string
		limit int
		want  []string
	}{
		{name: "no limit returns all", limit: 0, want: []string{"span-0", "span-1", "span-2", "span-3", "span-4"}},
		{name: "limit truncates", limit: 2, want: []string{"span-0", "span-1"}},
		{name: "limit above the size returns all", limit: 99, want: []string{"span-0", "span-1", "span-2", "span-3", "span-4"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Spans(context.Background(), SpanQuery{Limit: tc.limit})
			if err != nil {
				t.Fatalf("Spans() error = %v", err)
			}
			if !memEqualStrings(memSpanIDs(got), tc.want) {
				t.Fatalf("Spans() = %v, want %v", memSpanIDs(got), tc.want)
			}
		})
	}
}

// TestMemoryStoreCancelledContext checks that a cancelled context is
// reported instead of being ignored.
func TestMemoryStoreCancelledContext(t *testing.T) {
	s := NewMemoryStore(4)
	s.AcceptSpan(memRec("trace-a", "span-a", 0, nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := s.Spans(ctx, SpanQuery{})
	if err == nil {
		t.Fatalf("Spans() error = nil, want context.Canceled")
	}
	if err != context.Canceled {
		t.Fatalf("Spans() error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("Spans() returned %d spans on a cancelled context, want none", len(got))
	}
}

// TestMemoryStoreConcurrentAccess hammers the store from many
// goroutines: the race detector must stay quiet and the ring must stay
// bounded.
func TestMemoryStoreConcurrentAccess(t *testing.T) {
	const (
		capacity = 64
		writers  = 200
		perWrite = 20
	)

	s := NewMemoryStore(capacity)

	var writerWG sync.WaitGroup
	writerWG.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer writerWG.Done()
			for j := 0; j < perWrite; j++ {
				s.AcceptSpan(memRec(
					fmt.Sprintf("trace-%d", i%4),
					fmt.Sprintf("span-%d-%d", i, j),
					time.Duration(j)*time.Millisecond,
					map[string]string{"kyurem.hop": "1"},
				))
			}
		}(i)
	}

	// A reader runs concurrently with the whole write burst.
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := s.Spans(context.Background(), SpanQuery{TraceID: "trace-1"}); err != nil {
				t.Errorf("Spans() error = %v", err)
				return
			}
			_ = s.Len()
		}
	}()

	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	if got := s.Len(); got > capacity {
		t.Fatalf("Len() = %d, want at most %d", got, capacity)
	}
	if got := s.Len(); got != capacity {
		t.Fatalf("Len() = %d, want %d after %d writes", got, capacity, writers*perWrite)
	}
}
