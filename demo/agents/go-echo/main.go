// Demo echo agent (M1).
//
// A minimal A2A agent using the official a2a-go SDK. It exists to exercise
// the Kyurem gateway:
//
//   - Non-streaming requests reply with a single agent message:
//     "echo: <input>".
//   - Streaming requests (SendStreamingMessage) run a full task lifecycle:
//     submitted -> working -> one artifact event per word -> completed.
//     The artifact chunks reassemble to exactly "echo: <input>", mirroring
//     the non-streaming reply. The small inter-chunk sleep makes SSE flushing
//     observable through the gateway.
//
// Layout mirrors the SDK helloworld JSON-RPC example: JSON-RPC at the URL
// root, the agent card at /.well-known/agent-card.json. The card interface
// URL points at this process; the gateway rewrites URL when serving cards.
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
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

var (
	port = flag.Int("port", 9010, "port to listen on")
	host = flag.String("host", "127.0.0.1", "interface to listen on (0.0.0.0 inside containers)")
)

const (
	// replyPrefix marks every reply so gateway tests (and the chained
	// go-upper demo agent) can prove text went through this agent.
	replyPrefix = "echo: "
	// chunkDelay spaces streamed artifacts so SSE flushing is observable.
	chunkDelay = 40 * time.Millisecond
)

func main() {
	flag.Parse()

	card := &a2a.AgentCard{
		Name:        "go-echo",
		Description: "Demo M1 echo agent: replies with 'echo: <input>'; streams word by word.",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(fmt.Sprintf("http://127.0.0.1:%d", *port), a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Skills: []a2a.AgentSkill{
			{
				ID:          "echo",
				Name:        "Echo",
				Description: "Returns the caller's text prefixed with 'echo: '.",
				Tags:        []string{"echo", "demo"},
				Examples:    []string{"hello gateway", "kyurem m1"},
			},
		},
	}

	handler := a2asrv.NewHandler(echoExecutor())

	listener, err := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(*port)))
	if err != nil {
		log.Fatalf("bind failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	slog.Info("go-echo listening", "addr", listener.Addr().String())
	if err := http.Serve(listener, mux); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

// echoExecutor implements the agent behavior described in the package doc.
func echoExecutor() a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			if ec.Message == nil {
				yield(nil, fmt.Errorf("echo expects a message"))
				return
			}
			input := textOf(ec.Message)
			// Never log payloads; log shape only.
			slog.Debug("echo request", "text_len", len(input))

			if cc, ok := a2asrv.CallContextFrom(ctx); ok && cc.Method() == "SendStreamingMessage" {
				streamEcho(ec, input, yield)
				return
			}
			// Non-streaming: a single agent message terminates processing
			// per the A2A event contract.
			yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(replyPrefix+input)), nil)
		}
	})
}

// streamEcho emits the full task lifecycle: submitted -> working ->
// per-word artifacts -> completed. Per a2asrv docs the initial submitted
// task frames the user message as task history.
func streamEcho(ec *a2asrv.ExecutorContext, input string, yield func(a2a.Event, error) bool) {
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
			time.Sleep(chunkDelay)
		}
		// The first artifact carries the marker; the rest append words so
		// the joined text equals the non-streaming reply.
		var text string
		if artifactID == "" {
			text = replyPrefix + word
		} else {
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
}

// textOf concatenates the text parts of a message in order.
func textOf(m *a2a.Message) string {
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text())
	}
	return b.String()
}
