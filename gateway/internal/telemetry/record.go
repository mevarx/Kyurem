package telemetry

import (
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// SpanRecord is the normalized, backend-agnostic view of a finished span.
//
// The management API and the storage drivers read spans through this type,
// so the query layer never depends on SDK span types and the same query
// semantics work against any backend (in-memory ring, ClickHouse, ...).
// Attribute values are flattened to strings: dashboards and aggregations
// only ever need scalar attributes.
type SpanRecord struct {
	TraceID       string
	SpanID        string
	ParentSpanID  string
	Name          string
	Kind          string
	ServiceName   string
	StartedAt     time.Time
	EndedAt       time.Time
	StatusCode    string
	StatusMessage string
	// Attributes holds the span attributes (kyurem.*, gen_ai.*, http.*).
	Attributes map[string]string
	// ResourceAttributes holds the resource attributes (service.name, the
	// pinned semantic-convention commit, ...).
	ResourceAttributes map[string]string
}

// Duration is the wall-clock length of the span.
func (r SpanRecord) Duration() time.Duration {
	if r.EndedAt.IsZero() || r.StartedAt.IsZero() {
		return 0
	}
	return r.EndedAt.Sub(r.StartedAt)
}

// Sink receives finished spans. Implementations must never block the
// caller: the telemetry pipeline runs on the request path and a slow sink
// would slow the A2A data path.
type Sink interface {
	AcceptSpan(SpanRecord)
}

// RecordFromSpan converts a finished SDK span into a SpanRecord.
func RecordFromSpan(s sdktrace.ReadOnlySpan) SpanRecord {
	rec := SpanRecord{
		TraceID:            s.SpanContext().TraceID().String(),
		SpanID:             s.SpanContext().SpanID().String(),
		Name:               s.Name(),
		Kind:               s.SpanKind().String(),
		StartedAt:          s.StartTime(),
		EndedAt:            s.EndTime(),
		StatusCode:         s.Status().Code.String(),
		StatusMessage:      s.Status().Description,
		Attributes:         make(map[string]string, len(s.Attributes())),
		ResourceAttributes: make(map[string]string),
	}
	if parent := s.Parent(); parent.IsValid() {
		rec.ParentSpanID = parent.SpanID().String()
	}
	for _, kv := range s.Attributes() {
		rec.Attributes[string(kv.Key)] = kv.Value.Emit()
	}
	if res := s.Resource(); res != nil {
		for _, kv := range res.Attributes() {
			rec.ResourceAttributes[string(kv.Key)] = kv.Value.Emit()
		}
	}
	rec.ServiceName = rec.ResourceAttributes["service.name"]
	return rec
}
