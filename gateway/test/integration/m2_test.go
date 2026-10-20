// M2 acceptance tests (TR-1..TR-3, TR-5, TR-6): one A2A task renders as a
// single trace tree with one handoff span per gateway hop, and spans carry
// the caller/callee/task/context/status/byte attributes the dashboard needs.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

func attrMap(span tracetest.SpanStub) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(span.Attributes))
	for _, kv := range span.Attributes {
		out[string(kv.Key)] = kv.Value
	}
	return out
}

func spanByName(t *testing.T, spans tracetest.SpanStubs, name string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q not found in %d exported spans", name, len(spans))
	return tracetest.SpanStub{}
}

func forceFlush(t *testing.T, tel *telemetry.Telemetry) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tel.ForceFlush(ctx); err != nil {
		t.Fatalf("force flush spans: %v", err)
	}
}

// waitForSpans tolerates the small window between the client reading the
// last byte and the proxy handler ending the handoff span.
func waitForSpans(t *testing.T, tel *telemetry.Telemetry, exporter *tracetest.InMemoryExporter, want int) tracetest.SpanStubs {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		forceFlush(t, tel)
		spans := exporter.GetSpans()
		if len(spans) >= want {
			return spans
		}
		if time.Now().After(deadline) {
			t.Fatalf("exported spans = %d, want %d", len(spans), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TR-1/TR-2/TR-3/TR-6: client -> gateway -> upper -> gateway -> echo yields
// two handoff spans in one trace, with the echo span parented to the upper
// span, GenAI naming/attributes and caller attribution inferred from the
// trace tree (upper's SDK call does not need gateway-specific code).
func TestTraceTreeAcrossChainedAgents(t *testing.T) {
	echo := startEchoAgent(t)
	upper := newAgentServer(t)
	exporter := tracetest.NewInMemoryExporter()
	gw := startGatewayWithExporter(t, agentEntries(echo.URL(), upper.URL()), exporter)
	upper.setDelegate(upperAgentHandler(t, gw.URL()+"/a2a/echo"+cardSuffix))

	cancel, client := clientFor(t, gw.URL(), "upper")
	defer cancel()

	reply, err := sendText(context.Background(), client, "hello trace tree")
	if err != nil {
		t.Fatalf("send via upper: %v", err)
	}
	if reply != upperPrefix+"HELLO TRACE TREE" {
		t.Fatalf("reply = %q", reply)
	}
	spans := waitForSpans(t, gw.tel, exporter, 2)
	if len(spans) != 2 {
		t.Fatalf("exported spans = %d, want one per gateway hop", len(spans))
	}
	upperSpan := spanByName(t, spans, telemetry.InvokeAgentSpanName("upper"))
	echoSpan := spanByName(t, spans, telemetry.InvokeAgentSpanName("echo"))

	if upperSpan.SpanKind != trace.SpanKindServer || echoSpan.SpanKind != trace.SpanKindServer {
		t.Fatalf("span kinds = %v/%v, want SERVER", upperSpan.SpanKind, echoSpan.SpanKind)
	}
	if upperSpan.SpanContext.TraceID() != echoSpan.SpanContext.TraceID() {
		t.Fatalf("spans landed in different traces: %s vs %s",
			upperSpan.SpanContext.TraceID(), echoSpan.SpanContext.TraceID())
	}
	if upperSpan.Parent.IsValid() {
		t.Fatalf("upper handoff should root the trace for this SDK client, parent = %v", upperSpan.Parent)
	}
	if echoSpan.Parent.TraceID() != upperSpan.SpanContext.TraceID() ||
		echoSpan.Parent.SpanID() != upperSpan.SpanContext.SpanID() {
		t.Fatalf("echo parent = %v, want upper span %v", echoSpan.Parent, upperSpan.SpanContext)
	}

	upperAttrs := attrMap(upperSpan)
	echoAttrs := attrMap(echoSpan)
	for _, span := range []struct {
		stub   tracetest.SpanStub
		attrs  map[string]attribute.Value
		callee string
		caller string
		hop    int64
	}{
		{upperSpan, upperAttrs, "upper", telemetry.CallerUnknown, 0},
		{echoSpan, echoAttrs, "echo", "upper", 1},
	} {
		if got := span.attrs[telemetry.AttrOperationName].AsString(); got != telemetry.OperationInvokeAgent {
			t.Errorf("%s gen_ai.operation.name = %q", span.callee, got)
		}
		if got := span.attrs[telemetry.AttrProviderName].AsString(); got != telemetry.ProviderKyurem {
			t.Errorf("%s gen_ai.provider.name = %q", span.callee, got)
		}
		if got := span.attrs[telemetry.AttrAgentName].AsString(); got != span.callee {
			t.Errorf("%s gen_ai.agent.name = %q", span.callee, got)
		}
		if got := span.attrs[telemetry.AttrCalleeAgent].AsString(); got != span.callee {
			t.Errorf("%s kyurem.callee.agent = %q", span.callee, got)
		}
		if got := span.attrs[telemetry.AttrCallerAgent].AsString(); got != span.caller {
			t.Errorf("%s kyurem.caller.agent = %q, want %q", span.callee, got, span.caller)
		}
		if got := span.attrs[telemetry.AttrHopIndex].AsInt64(); got != span.hop {
			t.Errorf("%s kyurem.hop.index = %d, want %d", span.callee, got, span.hop)
		}
		if got := span.attrs[telemetry.AttrCardVerified].AsString(); got != telemetry.CardVerifiedUnchecked {
			t.Errorf("%s kyurem.card.verified = %q", span.callee, got)
		}
		if got := span.attrs[telemetry.AttrTokensKnown].AsBool(); got {
			t.Errorf("%s kyurem.tokens.known = true, M2 must mark usage unknown", span.callee)
		}
		if got := span.attrs[telemetry.AttrHTTPStatusCode].AsInt64(); got != 200 {
			t.Errorf("%s http.response.status_code = %d", span.callee, got)
		}
		if span.attrs[telemetry.AttrBytesIn].AsInt64() <= 0 || span.attrs[telemetry.AttrBytesOut].AsInt64() <= 0 {
			t.Errorf("%s byte attributes missing: in=%v out=%v", span.callee,
				span.attrs[telemetry.AttrBytesIn], span.attrs[telemetry.AttrBytesOut])
		}
		if span.attrs[telemetry.AttrLatencyMS].AsFloat64() < 0 {
			t.Errorf("%s negative latency", span.callee)
		}
		if span.stub.Status.Code != codes.Unset {
			t.Errorf("%s status = %v, want unset for a successful handoff", span.callee, span.stub.Status)
		}
	}
}

// TR-3: a streaming task still ends with one span carrying the final A2A
// task identity and status, without buffering the stream.
func TestStreamingHandoffSpanRecordsTaskIdentity(t *testing.T) {
	echo := startEchoAgent(t)
	exporter := tracetest.NewInMemoryExporter()
	gw := startGatewayWithExporter(t, agentEntries(echo.URL(), "http://127.0.0.1:1"), exporter)

	cancel, client := clientFor(t, gw.URL(), "echo")
	defer cancel()

	ctx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	for event, err := range client.SendStreamingMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("one two three")),
	}) {
		if err != nil {
			t.Fatalf("stream failed: %v", err)
		}
		if event == nil {
			continue
		}
	}
	spans := waitForSpans(t, gw.tel, exporter, 1)
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	span := spans[0]
	attrs := attrMap(span)
	if got := attrs[telemetry.AttrTaskID].AsString(); got == "" {
		t.Error("kyurem.task.id missing from the streaming handoff")
	}
	contextID := attrs[telemetry.AttrContextID].AsString()
	if contextID == "" {
		t.Error("kyurem.context.id missing from the streaming handoff")
	}
	if got := attrs[telemetry.AttrConversationID].AsString(); got != contextID {
		t.Errorf("gen_ai.conversation.id = %q, want context id %q", got, contextID)
	}
	if got := attrs[telemetry.AttrTaskStatus].AsString(); got != "TASK_STATE_COMPLETED" {
		t.Errorf("kyurem.task.status = %q, want the final completed state", got)
	}
	if got := attrs[telemetry.AttrHTTPStatusCode].AsInt64(); got != 200 {
		t.Errorf("http.response.status_code = %d", got)
	}
	if attrs[telemetry.AttrBytesIn].AsInt64() <= 0 || attrs[telemetry.AttrBytesOut].AsInt64() <= 0 {
		t.Errorf("streaming bytes missing: in=%v out=%v",
			attrs[telemetry.AttrBytesIn], attrs[telemetry.AttrBytesOut])
	}
	if span.EndTime.Before(span.StartTime) {
		t.Error("streaming span has an inverted interval")
	}
}

// TR-3: failed handoffs are distinguishable in the trace (status, error.type,
// HTTP code) instead of looking like successful calls.
func TestFailedHandoffSpanIsMarkedError(t *testing.T) {
	agents := agentEntries("http://127.0.0.1:1", "http://127.0.0.1:1")
	exporter := tracetest.NewInMemoryExporter()
	gw := startGatewayWithExporter(t, agents, exporter)

	code, _, _ := postTrace(t, gw.URL()+"/a2a/echo", "", "", formatJSONRPCSendMessage("m2-fail", "nobody home"))
	if code != 502 {
		t.Fatalf("status = %d, want 502", code)
	}
	spans := waitForSpans(t, gw.tel, exporter, 1)
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	span := spans[0]
	attrs := attrMap(span)
	if span.Status.Code != codes.Error {
		t.Errorf("span status = %v, want Error", span.Status)
	}
	if got := attrs[telemetry.AttrErrorType].AsString(); got != "upstream_error" {
		t.Errorf("error.type = %q, want upstream_error", got)
	}
	if got := attrs[telemetry.AttrHTTPStatusCode].AsInt64(); got != 502 {
		t.Errorf("http.response.status_code = %d, want 502", got)
	}
}

// TR-2/TR-3: a streamed task keeps one identity across the chain. The root
// hop (upper) carries the task id, and both hops share the A2A context id,
// so one task is one trace tree with one conversation.
func TestChainedStreamingTaskKeepsOneContext(t *testing.T) {
	echo := startEchoAgent(t)
	upper := newAgentServer(t)
	exporter := tracetest.NewInMemoryExporter()
	gw := startGatewayWithExporter(t, agentEntries(echo.URL(), upper.URL()), exporter)
	upper.setDelegate(upperAgentHandler(t, gw.URL()+"/a2a/echo"+cardSuffix))

	cancel, client := clientFor(t, gw.URL(), "upper")
	defer cancel()

	ctx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	for event, err := range client.SendStreamingMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("chained task")),
	}) {
		if err != nil {
			t.Fatalf("stream failed: %v", err)
		}
		if event == nil {
			continue
		}
	}

	spans := waitForSpans(t, gw.tel, exporter, 2)
	if len(spans) != 2 {
		t.Fatalf("exported spans = %d, want one per gateway hop", len(spans))
	}
	upperSpan := spanByName(t, spans, telemetry.InvokeAgentSpanName("upper"))
	echoSpan := spanByName(t, spans, telemetry.InvokeAgentSpanName("echo"))
	upperAttrs := attrMap(upperSpan)
	echoAttrs := attrMap(echoSpan)

	if upperSpan.Parent.IsValid() {
		t.Errorf("upper hop should root the trace, parent = %v", upperSpan.Parent)
	}
	if echoSpan.Parent.SpanID() != upperSpan.SpanContext.SpanID() {
		t.Errorf("echo hop parent = %v, want upper span %v", echoSpan.Parent, upperSpan.SpanContext)
	}
	if got := upperAttrs[telemetry.AttrTaskID].AsString(); got == "" {
		t.Error("root hop has no task id")
	}
	if got := upperAttrs[telemetry.AttrTaskStatus].AsString(); got != "TASK_STATE_COMPLETED" {
		t.Errorf("root hop task status = %q, want TASK_STATE_COMPLETED", got)
	}
	rootContext := upperAttrs[telemetry.AttrContextID].AsString()
	if rootContext == "" {
		t.Fatal("root hop has no context id")
	}
	if got := echoAttrs[telemetry.AttrContextID].AsString(); got != rootContext {
		t.Errorf("nested hop context id = %q, want %q", got, rootContext)
	}
	if got := echoAttrs[telemetry.AttrConversationID].AsString(); got != rootContext {
		t.Errorf("nested hop gen_ai.conversation.id = %q, want %q", got, rootContext)
	}
	if got := echoAttrs[telemetry.AttrCallerAgent].AsString(); got != "upper" {
		t.Errorf("nested hop caller = %q, want upper", got)
	}
}

// Exported spans carry the pinned conventions as resource attributes so a
// stored trace can always be interpreted against the version it was emitted
// with (TR-6).
func TestExportedResourcePinsConventions(t *testing.T) {
	echo := startEchoAgent(t)
	exporter := tracetest.NewInMemoryExporter()
	gw := startGatewayWithExporter(t, agentEntries(echo.URL(), "http://127.0.0.1:1"), exporter)

	code, _, _ := postTrace(t, gw.URL()+"/a2a/echo", "", "", formatJSONRPCSendMessage("m2-res", "pin"))
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	spans := waitForSpans(t, gw.tel, exporter, 1)
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	res := spans[0].Resource
	if res == nil {
		t.Fatal("exported span has no resource")
	}
	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got[telemetry.AttrGenAISemconvCommit] != telemetry.GenAISemconvCommit {
		t.Errorf("semconv commit = %q, want %q", got[telemetry.AttrGenAISemconvCommit], telemetry.GenAISemconvCommit)
	}
	if got[telemetry.AttrGenAISemconvRepo] != telemetry.GenAISemconvRepo {
		t.Errorf("semconv repo = %q", got[telemetry.AttrGenAISemconvRepo])
	}
	if got["service.name"] != "kyurem-gateway" {
		t.Errorf("service.name = %q", got["service.name"])
	}
}
