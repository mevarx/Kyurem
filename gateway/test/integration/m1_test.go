// M1 acceptance tests (GW-1..GW-7). Each test starts real SDK demo agents
// behind the real proxy handler and drives them with the official a2a-go
// SDK client (raw HTTP for wire-level probes), so behavior matches A2A v1.0.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

func agentEntries(echoURL, upperURL string) []config.AgentReg {
	return []config.AgentReg{
		{
			Name:      "echo",
			Aliases:   []string{"greeter"},
			Upstreams: []config.Upstream{{URL: echoURL, Binding: "JSONRPC"}},
		},
		{
			Name:      "upper",
			Upstreams: []config.Upstream{{URL: upperURL, Binding: "JSONRPC"}},
		},
	}
}

// GW-1/GW-2: a plain SendMessage through the gateway answers with the
// upstream agent's reply and the SDK client receives it unmodified.
func TestEchoRoundtripThroughGateway(t *testing.T) {
	agents := agentEntries("", "http://127.0.0.1:1")
	agents[0].Upstreams[0].URL = startEchoAgent(t).URL()
	agents[1].Upstreams[0].URL = "http://127.0.0.1:1"
	gw := startGateway(t, agents)

	cancel, client := clientFor(t, gw.URL(), "echo")
	defer cancel()

	reply, err := sendText(context.Background(), client, "hello gateway")
	if err != nil {
		t.Fatalf("send message via gateway: %v", err)
	}
	if reply != echoPrefix+"hello gateway" {
		t.Fatalf("reply = %q, want %q", reply, echoPrefix+"hello gateway")
	}
}

// GW-1: alias routes reach the same agent as the primary name.
func TestAliasRoutingThroughGateway(t *testing.T) {
	agents := agentEntries("", "http://127.0.0.1:1")
	agents[0].Upstreams[0].URL = startEchoAgent(t).URL()
	agents[1].Upstreams[0].URL = "http://127.0.0.1:1"
	gw := startGateway(t, agents)

	cancel, client := clientFor(t, gw.URL(), "greeter")
	defer cancel()

	reply, err := sendText(context.Background(), client, "alias check")
	if err != nil {
		t.Fatalf("send message via gateway alias: %v", err)
	}
	if reply != echoPrefix+"alias check" {
		t.Fatalf("reply = %q, want %q", reply, echoPrefix+"alias check")
	}
}

// GW-2: a streaming request tunnels end to end: chunked artifact events
// arrive before the terminal status and reassemble to the reply text.
func TestStreamingRoundtripThroughGateway(t *testing.T) {
	agents := agentEntries("", "http://127.0.0.1:1")
	agents[0].Upstreams[0].URL = startEchoAgent(t).URL()
	agents[1].Upstreams[0].URL = "http://127.0.0.1:1"
	gw := startGateway(t, agents)

	cancel, client := clientFor(t, gw.URL(), "echo")
	defer cancel()

	ctx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	var chunks []string
	sawWorking, sawCompleted := false, false
	for event, err := range client.SendStreamingMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello streaming gateway")),
	}) {
		if err != nil {
			t.Fatalf("stream failed mid-flight: %v", err)
		}
		switch v := event.(type) {
		case *a2a.Task:
			// The submitted task opens the stream.
		case *a2a.TaskStatusUpdateEvent:
			switch v.Status.State {
			case a2a.TaskStateWorking:
				sawWorking = true
			case a2a.TaskStateCompleted:
				sawCompleted = true
			}
		case *a2a.TaskArtifactUpdateEvent:
			for _, p := range v.Artifact.Parts {
				chunks = append(chunks, p.Text())
			}
		}
	}

	if !sawWorking {
		t.Errorf("streaming missed the working status event")
	}
	if !sawCompleted {
		t.Errorf("streaming missed the completed status event")
	}
	if len(chunks) < 3 {
		t.Errorf("streaming chunks = %d, want >= 3 (one per word)", len(chunks))
	}
	if got := strings.Join(chunks, ""); got != echoPrefix+"hello streaming gateway" {
		t.Errorf("joined chunks = %q, want %q", got, echoPrefix+"hello streaming gateway")
	}
	if chunks[0] != echoPrefix+"hello" {
		t.Errorf("first chunk = %q, want marker-bearing %q", chunks[0], echoPrefix+"hello")
	}
}

// GW-3: discovery through the gateway returns the upstream card with
// interface URLs rewritten back to the gateway route that served it.
func TestAgentCardRewrittenThroughGateway(t *testing.T) {
	agents := agentEntries("", "http://127.0.0.1:1")
	agents[0].Upstreams[0].URL = startEchoAgent(t).URL()
	agents[1].Upstreams[0].URL = "http://127.0.0.1:1"
	gw := startGateway(t, agents)

	resp, err := http.Get(gw.URL() + "/a2a/echo" + cardSuffix)
	if err != nil {
		t.Fatalf("GET card via gateway: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("card status = %d, want 200", resp.StatusCode)
	}

	var card a2a.AgentCard
	if err := jsonUnmarshalBody(resp, &card); err != nil {
		t.Fatalf("decode gateway card: %v", err)
	}
	if card.Name != "go-echo" {
		t.Errorf("card name = %q, want go-echo", card.Name)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("interfaces = %d, want 1", len(card.SupportedInterfaces))
	}
	iface := card.SupportedInterfaces[0]
	if iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("binding = %q, want JSONRPC", iface.ProtocolBinding)
	}
	if want := gw.URL() + "/a2a/echo"; iface.URL != want {
		t.Errorf("rewritten interface URL = %q, want %q", iface.URL, want)
	}
}

// GW-5: trace context rules at the integration seam. The SDK client cannot
// originate trace headers, so this test drives raw JSON-RPC posts through
// the gateway: (a) inbound traceparent is relayed with the same trace-id,
// a fresh span-id and verbatim tracestate; (b) absent inbound headers start
// a well-formed gateway root trace.
func TestTraceContextPropagationRule(t *testing.T) {
	traceCapture := &traceCapture{}
	echo := startEchoAgent(t)
	captured := newAgentServer(t)
	captured.setDelegate(captureMiddleware(echo.handler(), traceCapture))
	gw := startGateway(t, agentEntries(captured.URL(), "http://127.0.0.1:1"))

	const inTraceID = "1234567890abcdef1234567890abcdef"
	const inSpanID = "89abcdef89abcdef"

	t.Run("derive_from_inbound", func(t *testing.T) {
		traceCapture.reset()
		body := formatJSONRPCSendMessage("t1", "trace")
		code, _, _ := postTrace(t, gw.URL()+"/a2a/echo", "00-"+inTraceID+"-"+inSpanID+"-01", "kyurem=e1", body)
		if code != http.StatusOK {
			t.Fatalf("gateway status = %d, want 200", code)
		}
		parent, state := traceCapture.headers()
		if parent == "" {
			t.Fatalf("no traceparent reached the upstream agent")
		}
		parts := strings.Split(parent, "-")
		if len(parts) != 4 || parts[0] != "00" || parts[1] != inTraceID || parts[3] != "01" {
			t.Fatalf("upstream traceparent %q: want same trace-id %q, version 00, flags 01", parent, inTraceID)
		}
		if parts[2] == inSpanID {
			t.Errorf("upstream span-id %q must be freshly derived, not the inbound span", parts[2])
		}
		if state != "kyurem=e1" {
			t.Errorf("upstream tracestate = %q, want verbatim %q", state, "kyurem=e1")
		}
	})

	t.Run("mint_root_when_absent", func(t *testing.T) {
		traceCapture.reset()
		body := formatJSONRPCSendMessage("t2", "trace")
		code, _, _ := postTrace(t, gw.URL()+"/a2a/echo", "", "", body)
		if code != http.StatusOK {
			t.Fatalf("gateway status = %d, want 200", code)
		}
		parent, state := traceCapture.headers()
		if !validTraceparentPattern.MatchString(parent) {
			t.Fatalf("minted traceparent = %q, want ^00-<32hex>-<16hex>-01$", parent)
		}
		if state != "" {
			t.Errorf("tracestate %q must stay empty when none was inbound", state)
		}
	})
}

// GW-6: unreachable upstream fails visibly with 502 instead of hanging or
// leaking internals.
func TestUnreachableUpstreamReturns502(t *testing.T) {
	agents := agentEntries("", "http://127.0.0.1:1")
	agents[0].Upstreams[0].URL = "http://127.0.0.1:1" // reserved, nothing listens
	agents[1].Upstreams[0].URL = "http://127.0.0.1:1"
	gw := startGateway(t, agents)

	code, contentType, payload := postTrace(t, gw.URL()+"/a2a/echo", "", "", formatJSONRPCSendMessage("t3", "go"))
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", code)
	}
	if contentType != "application/json" {
		t.Errorf("error content type = %q, want application/json", contentType)
	}
	if !strings.Contains(payload, `"error":"upstream_error"`) {
		t.Errorf("502 body %q misses the upstream_error kind", payload)
	}
}

func TestUpperChainsEchoThroughGateway(t *testing.T) {
	echo := startEchoAgent(t)
	// Upper's own upstream URL must exist before the registry builds, but
	// its handler needs the gateway URL — install the delegate afterwards.
	upper := newAgentServer(t)
	gw := startGateway(t, agentEntries(echo.URL(), upper.URL()))
	upper.setDelegate(upperAgentHandler(t, gw.URL()+"/a2a/echo"+cardSuffix))

	cancel, client := clientFor(t, gw.URL(), "upper")
	defer cancel()

	reply, err := sendText(context.Background(), client, "hello gateway")
	if err != nil {
		t.Fatalf("send message via gateway upper: %v", err)
	}
	if reply != upperPrefix+"HELLO GATEWAY" {
		t.Fatalf("reply = %q, want %q", reply, upperPrefix+"HELLO GATEWAY")
	}
}

// validTraceparentPattern accepts the minted gateway root form.
var validTraceparentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

func formatJSONRPCSendMessage(id, text string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"method":"SendMessage","params":{"message":{"messageId":"%s","role":"ROLE_USER","parts":[{"kind":"text","text":%q}]}}}`,
		id, id, text)
}

// postTrace posts a raw JSON-RPC SendMessage with explicit trace headers.
func postTrace(t *testing.T, url, traceparent, tracestate, body string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if traceparent != "" {
		req.Header.Set("Traceparent", traceparent)
	}
	if tracestate != "" {
		req.Header.Set("Tracestate", tracestate)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post via gateway: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read gateway response: %v", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(raw)
}

func jsonUnmarshalBody(resp *http.Response, v any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
