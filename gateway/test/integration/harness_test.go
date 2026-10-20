// Package integration wires the gateway's in-process M1 stack:
// SDK-backed demo agents (echo / upper) -> Kyurem proxy handler over
// httptest, driven by the official a2a-go SDK client — the same lockstep
// the m1-basic.sh demo performs, executable from `go test`.
package integration

import (
	"context"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/proxy"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
)

const (
	cardSuffix    = a2asrv.WellKnownAgentCardPath
	echoPrefix    = "echo: "
	upperPrefix   = "upper: "
	streamBarrier = "SendStreamingMessage" // a2asrv CallContext method name
)

// agentServer hosts an A2A agent in-process. The delegate handler is
// installed after the base server has a URL, so the static card can
// advertise the real address.
type agentServer struct {
	base *httptest.Server
	h    atomic.Pointer[http.Handler]
}

func (a *agentServer) handler() http.Handler {
	if h := a.h.Load(); h != nil {
		return *h
	}
	return nil
}

func (a *agentServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h := a.handler(); h != nil {
		h.ServeHTTP(w, r)
		return
	}
	http.Error(w, `{"error":"agent not ready"}`, http.StatusServiceUnavailable)
}

func (a *agentServer) setDelegate(h http.Handler) { a.h.Store(&h) }

func (a *agentServer) URL() string { return a.base.URL }

func newAgentServer(t *testing.T) *agentServer {
	t.Helper()
	a := &agentServer{}
	a.base = httptest.NewServer(a)
	t.Cleanup(a.base.Close)
	return a
}

// startEchoAgent runs the demo echo agent: replies "echo: <input>";
// SendStreamingMessage emits a full task lifecycle, one artifact per word.
func startEchoAgent(t *testing.T) *agentServer {
	t.Helper()
	a := newAgentServer(t)
	card := &a2a.AgentCard{
		Name:        "go-echo",
		Description: "Integration demo echo agent",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(a.base.URL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills: []a2a.AgentSkill{
			{ID: "echo", Name: "Echo", Description: "Echoes text"},
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(echoExecutor())))
	mux.Handle(cardSuffix, a2asrv.NewStaticAgentCardHandler(card))
	a.setDelegate(mux)
	return a
}

// startUpperAgent was replaced by upperAgentHandler.

// upperAgentHandler is the demo chained agent: forwards text to echo via
// the gateway (card discovery through the rewritten card), strips the echo
// marker and answers "upper: <UPPERCASED>". echoCardURL is the full echo
// card URL through the gateway.
func upperAgentHandler(t *testing.T, echoCardURL string) http.Handler {
	t.Helper()
	echoCard, err := agentcard.DefaultResolver.Resolve(context.Background(), echoCardURL)
	if err != nil {
		t.Fatalf("upper agent resolves echo card: %v", err)
	}
	echoClient, err := a2aclient.NewFromCard(context.Background(), echoCard)
	if err != nil {
		t.Fatalf("upper agent creates echo client: %v", err)
	}
	card := &a2a.AgentCard{
		Name:        "go-upper",
		Description: "Integration demo chained agent",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("", a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills: []a2a.AgentSkill{
			{ID: "upper", Name: "Uppercase", Description: "Uppercases text via echo"},
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(upperExecutor(echoClient))))
	mux.Handle(cardSuffix, a2asrv.NewStaticAgentCardHandler(card))
	return mux
}

func echoExecutor() a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			if ec.Message == nil {
				yield(nil, fmt.Errorf("echo expects a message"))
				return
			}
			input := textOf(ec.Message)
			if cc, ok := a2asrv.CallContextFrom(ctx); ok && cc.Method() == streamBarrier {
				if ec.StoredTask == nil {
					if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
						return
					}
				}
				if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
					return
				}
				words := strings.Fields(input)
				if len(words) == 0 {
					words = []string{""}
				}
				var artifactID a2a.ArtifactID
				for i, word := range words {
					if i > 0 {
						time.Sleep(5 * time.Millisecond)
					}
					text := echoPrefix + word
					if artifactID != "" {
						text = " " + word
					}
					var event *a2a.TaskArtifactUpdateEvent
					if artifactID == "" {
						event = a2a.NewArtifactEvent(ec, a2a.NewTextPart(text))
						artifactID = event.Artifact.ID
					} else {
						event = a2a.NewArtifactUpdateEvent(ec, artifactID, a2a.NewTextPart(text))
					}
					if !yield(event, nil) {
						return
					}
				}
				yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
				return
			}
			yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(echoPrefix+input)), nil)
		}
	})
}

func upperExecutor(echo *a2aclient.Client) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			if ec.Message == nil {
				yield(nil, fmt.Errorf("upper expects a message"))
				return
			}
			resp, err := echo.SendMessage(ctx, &a2a.SendMessageRequest{
				Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(textOf(ec.Message))),
			})
			if err != nil {
				yield(nil, fmt.Errorf("echo call failed: %w", err))
				return
			}
			payload, ok := echoPrefixCut(textOfResult(resp))
			if !ok {
				yield(nil, fmt.Errorf("echo reply missing the %q marker", echoPrefix))
				return
			}
			yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(upperPrefix+strings.ToUpper(payload))), nil)
		}
	})
}

func echoPrefixCut(text string) (string, bool) { return strings.CutPrefix(text, echoPrefix) }

func textOf(m *a2a.Message) string {
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text())
	}
	return b.String()
}

func textOfResult(res a2a.SendMessageResult) string {
	switch v := res.(type) {
	case *a2a.Message:
		return textOf(v)
	case *a2a.Task:
		var b strings.Builder
		if v.Status.Message != nil {
			b.WriteString(textOf(v.Status.Message))
		}
		for _, artifact := range v.Artifacts {
			for _, p := range artifact.Parts {
				b.WriteString(p.Text())
			}
		}
		return b.String()
	default:
		return ""
	}
}

// traceCapture records the last trace headers an upstream has seen.
type traceCapture struct {
	mu          sync.Mutex
	traceparent string
	tracestate  string
}

func (c *traceCapture) record(r *http.Request) {
	c.mu.Lock()
	c.traceparent = r.Header.Get("Traceparent")
	c.tracestate = r.Header.Get("Tracestate")
	c.mu.Unlock()
}

func (c *traceCapture) headers() (parent, state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.traceparent, c.tracestate
}

func (c *traceCapture) reset() {
	c.mu.Lock()
	c.traceparent, c.tracestate = "", ""
	c.mu.Unlock()
}

// captureMiddleware records trace headers, then delegates downstream.
func captureMiddleware(inner http.Handler, c *traceCapture) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.record(r)
		inner.ServeHTTP(w, r)
	})
}

// gwStack is the M1 gateway in-process: config -> registry -> card cache ->
// proxy handler under an httptest server.
type gwStack struct {
	base *httptest.Server
	reg  *registry.Registry
}

// startGateway builds the full wiring exactly as server.New does.
func startGateway(t *testing.T, agents []config.AgentReg) *gwStack {
	t.Helper()
	// Seed config passes startup validation; the agent list is replaced
	// with the runtime-resolved upstream URLs before the registry builds.
	cfg, err := config.LoadBytes([]byte(`
listeners:
  a2a:
    addr: "127.0.0.1:0"
  admin:
    addr: "127.0.0.1:0"
agents:
  - name: placeholder
    upstreams:
      - url: http://127.0.0.1:1
`))
	if err != nil {
		t.Fatalf("load seed config: %v", err)
	}
	cfg.Agents = agents
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mtr := metrics.New()
	reg, err := registry.New(cfg)
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	cache := registry.NewCardCache(
		reg, nil,
		cfg.CardCache.TTL.Std(), cfg.CardCache.StaleGrace.Std(), cfg.CardCache.MaxCardBytes,
		logger,
	)
	s := &gwStack{reg: reg}
	s.base = httptest.NewServer(proxy.New(cfg, reg, cache, mtr, logger))
	t.Cleanup(s.base.Close)
	return s
}

func (g *gwStack) URL() string { return g.base.URL }

// clientFor ends at the gateway route /a2a/{agent}, discarding the card the
// same way an external A2A client does: discovery first, then a SDK client.
func clientFor(t *testing.T, gwURL, agent string) (context.CancelFunc, *a2aclient.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cardURL := fmt.Sprintf("%s/a2a/%s%s", gwURL, agent, cardSuffix)
	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		cancel()
		t.Fatalf("resolve card via gateway %s: %v", cardURL, err)
	}
	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		cancel()
		t.Fatalf("create client via gateway: %v", err)
	}
	return cancel, client
}

// sendText sends one non-streaming text message and returns the reply text
// (message or task form).
func sendText(ctx context.Context, client *a2aclient.Client, text string) (string, error) {
	resp, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text)),
	})
	if err != nil {
		return "", err
	}
	return textOfResult(resp), nil
}
