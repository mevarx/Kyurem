// Demo upper agent (M1).
//
// A chained A2A agent that exercises server-to-server routing through the
// Kyurem gateway. Incoming text T is forwarded to the echo agent (through
// the gateway's /a2a/echo route by default), the reply must carry the echo
// marker, and upper answers with:
//
//	upper: <uppercase(T)>
//
// The marker check is the point: a reply that did not travel through the
// echo agent cannot produce the final text, so the demo scenario proves
// client -> gateway -> upper -> gateway -> echo end to end.
//
// The echo agent is discovered via its (rewritten) agent card — exactly how
// real A2A clients discover agents behind the gateway:
//
//	agentcard.DefaultResolver.Resolve(ctx, --echo-card-url)
//	a2aclient.NewFromCard(ctx, card)
//
// Start order matters: echo first, then the gateway (so card discovery can
// resolve), then this agent (it resolves the card once at startup).
package main

import (
	"context"
	"flag"
	"fmt"
	"iter"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

var (
	port        = flag.Int("port", 9011, "port to listen on")
	host        = flag.String("host", "127.0.0.1", "interface to listen on (0.0.0.0 inside containers)")
	echoCardURL = flag.String("echo-card-url",
		"http://127.0.0.1:8080/a2a/echo/.well-known/agent-card.json",
		"full agent card URL used to discover the echo agent (through the gateway)")
	echoCardWait = flag.Duration("echo-card-wait", 0,
		"keep retrying card discovery for this long (compose start ordering); 0 = one attempt")
)

const (
	replyPrefix = "upper: "
	echoPrefix  = "echo: "
	// streamMethod is the a2asrv call-context method name of the
	// streaming RPC (SendStreamingMessage).
	streamMethod = "SendStreamingMessage"
	// callerHeader is an untrusted attribution hint read by the gateway
	// when a nested agent cannot propagate its own span. The SDK call below
	// also forwards traceparent/tracestate, so this is only a fallback.
	callerHeader = "X-Kyurem-Caller"
	callerName   = "upper"
)

func main() {
	flag.Parse()

	ctx := context.Background()
	if *echoCardWait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *echoCardWait)
		defer cancel()
	}
	echo, err := connectEchoWithWait(ctx, *echoCardURL, *echoCardWait)
	if err != nil {
		log.Fatalf("resolve echo agent card at %s: %v", *echoCardURL, err)
	}
	slog.Info("echo agent connected", "card_url", *echoCardURL)

	card := &a2a.AgentCard{
		Name:        "go-upper",
		Description: "Demo M1 chained agent: echoes through the gateway, replies with 'upper: <UPPERCASED>'.",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(fmt.Sprintf("http://127.0.0.1:%d", *port), a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills: []a2a.AgentSkill{
			{
				ID:          "upper",
				Name:        "Uppercase",
				Description: "Uppercases the caller's text after a round trip through the echo agent.",
				Tags:        []string{"upper", "demo", "chained"},
				Examples:    []string{"hello gateway"},
			},
		},
	}

	handler := a2asrv.NewHandler(upperExecutor(echo))

	listener, err := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(*port)))
	if err != nil {
		log.Fatalf("bind failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	slog.Info("go-upper listening", "addr", listener.Addr().String())
	if err := http.Serve(listener, mux); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

// connectEcho resolves the echo agent card and builds a reusable client.
// The card served by the gateway rewrites interface URLs back to the
// gateway, so every echo call flows through Kyurem.
func connectEcho(ctx context.Context, cardURL string) (*a2aclient.Client, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		return nil, err
	}
	return a2aclient.NewFromCard(ctx, card)
}

// connectEchoWithWait tolerates compose start ordering: the gateway and
// echo may still be booting when this agent starts. A non-positive wait is
// a single attempt, preserving the M1 local-demo behavior.
func connectEchoWithWait(ctx context.Context, cardURL string, wait time.Duration) (*a2aclient.Client, error) {
	deadline := time.Now().Add(wait)
	for {
		echo, err := connectEcho(ctx, cardURL)
		if err == nil {
			return echo, nil
		}
		if wait <= 0 || time.Now().After(deadline) {
			return nil, err
		}
		slog.Warn("echo agent card not ready; retrying", "card_url", cardURL, "err", err)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// upperExecutor returns the agent behavior: text -> echo (via gateway) ->
// marker check -> uppercase -> reply.
func upperExecutor(echo *a2aclient.Client) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			if ec.Message == nil {
				yield(nil, fmt.Errorf("upper expects a message"))
				return
			}
			input := textOf(ec.Message)
			slog.Debug("upper request", "text_len", len(input))

			// A streaming call runs a real task lifecycle so the gateway
			// sees a task id and a context id on this hop. The non-streaming
			// call answers with a plain agent message.
			streaming := false
			if cc, ok := a2asrv.CallContextFrom(ctx); ok && cc.Method() == streamMethod {
				streaming = true
			}
			if streaming && ec.StoredTask == nil {
				if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
					return
				}
			}

			// Nothing has been emitted yet, so failing here is allowed by
			// the A2A execution contract: the SDK turns this into a failed
			// task result for the caller.
			echoReply, err := callEcho(forwardTraceContext(ctx, ec), echo, ec, input)
			if err != nil {
				yield(nil, fmt.Errorf("echo agent call failed: %w", err))
				return
			}
			payload, ok := strings.CutPrefix(echoReply, echoPrefix)
			if !ok {
				yield(nil, fmt.Errorf("echo reply missing %q marker", echoPrefix))
				return
			}
			reply := replyPrefix + strings.ToUpper(payload)

			if streaming {
				if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
					return
				}
				if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(reply)), nil) {
					return
				}
				yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
				return
			}
			// Non-streaming: a single agent message terminates processing
			// per the A2A event contract.
			yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(reply)), nil)
		}
	})
}

// callEcho sends one non-streaming message through the gateway to echo and
// extracts the reply text whether echo answered with a message or a task.
// The task ID is deliberately not forwarded: it belongs to upper's task,
// which echo's task store does not know. The context ID is preserved so the
// whole chain shares one A2A conversation.
func callEcho(ctx context.Context, echo *a2aclient.Client, ec *a2asrv.ExecutorContext, text string) (string, error) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	msg.ContextID = ec.ContextID
	resp, err := echo.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return "", err
	}
	return textOfResult(resp), nil
}

// forwardTraceContext relays the inbound W3C trace context and adds the
// caller hint. a2aclient sends ServiceParams as HTTP headers, so gateway
// spans nested under this agent's hop stay in one trace tree without any
// gateway-specific SDK code.
func forwardTraceContext(ctx context.Context, ec *a2asrv.ExecutorContext) context.Context {
	params := a2aclient.ServiceParams{callerHeader: {callerName}}
	for _, header := range []string{"traceparent", "tracestate"} {
		if vals, ok := ec.ServiceParams.Get(header); ok && len(vals) > 0 {
			params[header] = vals
		}
	}
	return a2aclient.AttachServiceParams(ctx, params)
}

// textOfResult flattens SendMessageResult text: agent messages carry a
// message; the task template path carries it in status message and/or
// artifacts.
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

// textOf concatenates the text parts of a message in order.
func textOf(m *a2a.Message) string {
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text())
	}
	return b.String()
}
