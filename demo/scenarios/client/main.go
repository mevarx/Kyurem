// Demo client (M1).
//
// A thin A2A CLI for exercising the Kyurem gateway end to end. Given a
// gateway agent route (e.g. http://127.0.0.1:8080/a2a/echo) it discovers
// the agent via the card the gateway rewrites and routes back, then sends
// one message — non-streaming or SSE streaming.
//
// Output is line-based and script friendly; the final line is always
//
//	RESULT: <reply text>
//
// so scenario scripts can assert the round trip (and, chained through
// go-upper, prove agent-to-agent routing across the gateway).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

var (
	gatewayURL = flag.String("gateway-url", "http://127.0.0.1:8080/a2a/echo",
		"gateway agent route (card is discovered at <url>/.well-known/agent-card.json)")
	message = flag.String("message", "hello gateway", "text to send")
	stream  = flag.Bool("stream", false, "use SendStreamingMessage (SSE)")
	timeout = flag.Duration("timeout", 30*time.Second, "overall request timeout")
)

func main() {
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	userMsg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(*message))

	cardURL := strings.TrimSuffix(*gatewayURL, "/") + "/.well-known/agent-card.json"
	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		log.Fatalf("resolve agent card at %s: %v", cardURL, err)
	}
	fmt.Printf("agent=%s protocols=%s\n", card.Name, bindings(card))

	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		log.Fatalf("create client: %v", err)
	}

	if *stream {
		var chunks strings.Builder
		for event, err := range client.SendStreamingMessage(ctx, &a2a.SendMessageRequest{Message: userMsg}) {
			if err != nil {
				log.Fatalf("stream failed: %v", err)
			}
			chunk, text := describeEvent(event)
			if chunk != "" {
				fmt.Printf("chunk:%s\n", chunk)
			}
			if text != "" {
				chunks.WriteString(text)
			}
		}
		fmt.Printf("RESULT: %s\n", strings.TrimSpace(chunks.String()))
		return
	}

	resp, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: userMsg})
	if err != nil {
		log.Fatalf("send message failed: %v", err)
	}
	switch v := resp.(type) {
	case *a2a.Message:
		fmt.Printf("message id=%s\n", v.ID)
		fmt.Printf("RESULT: %s\n", textOf(v))
	case *a2a.Task:
		fmt.Printf("task id=%s state=%s\n", v.ID, v.Status.State)
		fmt.Printf("RESULT: %s\n", taskText(v))
	default:
		fmt.Printf("RESULT: NULL\n")
	}
}

// bindings lists the ProtocolBinding values advertised in the card, on a
// single line for easy grepping in scenarios.
func bindings(card *a2a.AgentCard) string {
	names := make([]string, 0, len(card.SupportedInterfaces))
	for _, iface := range card.SupportedInterfaces {
		names = append(names, string(iface.ProtocolBinding))
	}
	return strings.Join(names, ",")
}

// describeEvent turns one streamed event into (chunk-line, accumulated text).
// Only artifact text accumulates; task/status details are informational.
func describeEvent(event a2a.Event) (chunk, text string) {
	switch v := event.(type) {
	case *a2a.Task:
		return fmt.Sprintf("task id=%s state=%s", v.ID, v.Status.State), ""
	case *a2a.TaskStatusUpdateEvent:
		return fmt.Sprintf("state state=%s", v.Status.State), ""
	case *a2a.TaskArtifactUpdateEvent:
		t := partText(v.Artifact.Parts)
		return t, t
	case *a2a.Message:
		return "-terminate-", textOf(v)
	default:
		return "", ""
	}
}

// taskText flattens a task's status message and artifacts into one string.
func taskText(t *a2a.Task) string {
	var b strings.Builder
	if t.Status.Message != nil {
		b.WriteString(textOf(t.Status.Message))
	}
	for _, artifact := range t.Artifacts {
		b.WriteString(partText(artifact.Parts))
	}
	return b.String()
}

func partText(parts []*a2a.Part) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text())
	}
	return b.String()
}

func textOf(m *a2a.Message) string {
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text())
	}
	return b.String()
}
