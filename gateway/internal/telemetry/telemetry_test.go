package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

func traceSpanContext(ctx context.Context) trace.SpanContext {
	return trace.SpanContextFromContext(ctx)
}

func TestExtractInjectPreservesTraceAndTracestate(t *testing.T) {
	tel, err := NewWithExporter(context.Background(), config.Telemetry{}, testLogger(), nil, nil)
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	defer func() { _ = tel.Shutdown(context.Background()) }()

	carrier := propagation.MapCarrier{
		"traceparent": "00-" + testTraceID + "-" + testSpanID + "-01",
		"tracestate":  "vendor=value",
	}
	inbound := tel.Extract(context.Background(), carrier)
	parent := spanContext(t, testTraceID, testSpanID)
	if got := traceSpanContext(inbound); got.TraceID() != parent.TraceID() || got.SpanID() != parent.SpanID() {
		t.Fatalf("extracted context = %v, want parent %v", got, parent)
	}

	ctx, span := tel.StartSpan(inbound, "invoke_agent echo")
	if span.SpanContext().TraceID() != parent.TraceID() {
		t.Fatalf("span trace id = %s, want %s", span.SpanContext().TraceID(), parent.TraceID())
	}
	if span.SpanContext().SpanID() == parent.SpanID() {
		t.Fatal("handoff span must get a fresh span id")
	}
	if got := span.SpanContext().TraceState().String(); got != "vendor=value" {
		t.Fatalf("tracestate = %q, want vendor=value", got)
	}

	out := propagation.MapCarrier{}
	tel.Inject(ctx, out)
	if got := out.Get("traceparent"); got == "" {
		t.Fatal("inject wrote no traceparent")
	}
	if got := out.Get("tracestate"); got != "vendor=value" {
		t.Fatalf("injected tracestate = %q", got)
	}
	span.End()
}

func TestMissingParentMintsSampledRoot(t *testing.T) {
	tel, err := NewWithExporter(context.Background(), config.Telemetry{}, testLogger(), nil, nil)
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	defer func() { _ = tel.Shutdown(context.Background()) }()

	_, span := tel.StartSpan(context.Background(), "invoke_agent echo")
	defer span.End()

	sc := span.SpanContext()
	if !sc.IsValid() || !sc.IsSampled() {
		t.Fatalf("gateway root = %v, want valid sampled context", sc)
	}
	if sc.TraceState().Len() != 0 {
		t.Fatalf("root tracestate = %q, want empty", sc.TraceState().String())
	}
}

// ParentBased sampling: gateway-rooted traces follow sample_ratio, but an
// inbound sampled trace is always honored.
func TestSamplerHonorsParentAndRootRatio(t *testing.T) {
	zero := 0.0
	exporter := tracetest.NewInMemoryExporter()
	tel, err := NewWithExporter(context.Background(), config.Telemetry{SampleRatio: &zero}, testLogger(), nil, exporter)
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	defer func() { _ = tel.Shutdown(context.Background()) }()

	_, root := tel.StartSpan(context.Background(), "invoke_agent root")
	if root.SpanContext().IsSampled() {
		t.Fatal("ratio 0 must not sample a gateway root")
	}
	root.End()

	inbound := tel.Extract(context.Background(), propagation.MapCarrier{
		"traceparent": "00-" + testTraceID + "-" + testSpanID + "-01",
	})
	_, child := tel.StartSpan(inbound, "invoke_agent echo")
	if !child.SpanContext().IsSampled() {
		t.Fatal("inbound sampled parent must be honored regardless of ratio")
	}
	child.End()

	if err := tel.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want only the inbound child", len(spans))
	}
	if spans[0].Name != "invoke_agent echo" {
		t.Fatalf("exported span = %q", spans[0].Name)
	}
}

// The real OTLP HTTP exporter must deliver protobuf spans with the pinned
// resource attributes to a collector-shaped endpoint.
func TestOTLPHTTPExportDeliversSpans(t *testing.T) {
	type received struct {
		path string
		req  *collectortracev1.ExportTraceServiceRequest
	}
	var (
		mu   sync.Mutex
		got  []received
		done = make(chan struct{})
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req collectortracev1.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		got = append(got, received{path: r.URL.Path, req: &req})
		mu.Unlock()
		select {
		case <-done:
		default:
			close(done)
		}
		resp, _ := proto.Marshal(&collectortracev1.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	tel, err := New(context.Background(), config.Telemetry{
		OTLPEndpoint: srv.URL,
		OTLPProtocol: "http/protobuf",
	}, testLogger(), nil)
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	defer func() { _ = tel.Shutdown(context.Background()) }()

	_, span := tel.StartSpan(context.Background(), "invoke_agent echo",
		attribute.String(AttrOperationName, OperationInvokeAgent),
		attribute.String(AttrCalleeAgent, "echo"),
	)
	span.End()
	if err := tel.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collector received no export")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("exports = %d, want 1", len(got))
	}
	if got[0].path != "/v1/traces" {
		t.Fatalf("export path = %q, want /v1/traces", got[0].path)
	}
	rs := got[0].req.GetResourceSpans()
	if len(rs) != 1 || len(rs[0].GetScopeSpans()) != 1 || len(rs[0].GetScopeSpans()[0].GetSpans()) != 1 {
		t.Fatalf("unexpected OTLP shape: %v", rs)
	}
	resAttrs := map[string]string{}
	for _, kv := range rs[0].GetResource().GetAttributes() {
		resAttrs[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	if resAttrs["service.name"] != "kyurem-gateway" {
		t.Errorf("service.name = %q", resAttrs["service.name"])
	}
	if resAttrs[AttrGenAISemconvCommit] != GenAISemconvCommit {
		t.Errorf("semconv pin = %q, want %q", resAttrs[AttrGenAISemconvCommit], GenAISemconvCommit)
	}

	exported := rs[0].GetScopeSpans()[0].GetSpans()[0]
	if exported.GetName() != "invoke_agent echo" {
		t.Errorf("span name = %q", exported.GetName())
	}
	attrs := map[string]string{}
	for _, kv := range exported.GetAttributes() {
		attrs[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	if attrs[AttrOperationName] != OperationInvokeAgent || attrs[AttrCalleeAgent] != "echo" {
		t.Errorf("span attributes = %v", attrs)
	}
}

func TestNewRejectsUnknownProtocol(t *testing.T) {
	_, err := New(context.Background(), config.Telemetry{
		OTLPEndpoint: "127.0.0.1:4318",
		OTLPProtocol: "carrier-pigeon",
	}, testLogger(), nil)
	if err == nil {
		t.Fatal("expected an error for an unsupported protocol")
	}
}

func TestNewWithExporterNilStillCreatesProvider(t *testing.T) {
	tel, err := NewWithExporter(context.Background(), config.Telemetry{}, testLogger(), nil, nil)
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	if tel.Exporting() {
		t.Fatal("nil exporter must report export disabled")
	}
	if tel.Provider() == nil {
		t.Fatal("provider must exist for W3C propagation even without export")
	}
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
