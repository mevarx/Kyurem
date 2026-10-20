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
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

var (
	port        = flag.Int("port", 9011, "port to listen on")
	echoCardURL = flag.String("echo-card-url",
		"http://127.0.0.1:8080/a2a/echo/.well-known/agent-card.json",
		"full agent card URL used to discover the echo agent (through the gateway)")
)

const (
	replyPrefix = "upper: "
	echoPrefix  = "echo: "
)

func main() {
	flag.Parse()

	echo, err := connectEcho(context.Background(), *echoCardURL)
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

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
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

			// Nothing has been emitted yet, so failing here is allowed by
			// the A2A execution contract: the SDK turns this into a failed
			// task result for the caller.
			echoReply, err := callEcho(ctx, echo, input)
			if err != nil {
				yield(nil, fmt.Errorf("echo agent call failed: %w", err))
				return
			}
			payload, ok := strings.CutPrefix(echoReply, echoPrefix)
			if !ok {
				yield(nil, fmt.Errorf("echo reply missing %q marker", echoPrefix))
				return
			}
			// Non-streaming: a single agent message terminates processing
			// per the A2A event contract.
			yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(replyPrefix+strings.ToUpper(payload))), nil)
		}
	})
}

// callEcho sends one non-streaming message through the gateway to echo and
// extracts the reply text whether echo answered with a message or a task.
func callEcho(ctx context.Context, echo *a2aclient.Client, text string) (string, error) {
	resp, err := echo.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text)),
	})
	if err != nil {
		return "", err
	}
	return textOfResult(resp), nil
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
