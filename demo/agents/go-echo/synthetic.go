package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// syntheticStream is a minimal SSE upstream for capacity benchmarks: it
// emits the same event shapes as streamEcho without the A2A SDK's per-event
// task-store work, so the gateway rather than the demo agent is what a
// stream-count test measures. Non-streaming calls fall through to next.
func syntheticStream(next http.Handler, delay time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Message struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"message"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &req) != nil || req.Method != "SendStreamingMessage" {
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			next.ServeHTTP(w, r)
			return
		}
		var text string
		for _, p := range req.Params.Message.Parts {
			text += p.Text
		}
		words := strings.Fields(text)
		if len(words) == 0 {
			words = []string{""}
		}

		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		emit := func(result string) bool {
			if _, err := fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n\n", req.ID, result); err != nil {
				return false
			}
			fl.Flush()
			return true
		}
		const ids = `"taskId":"synthetic","contextId":"synthetic"`
		if !emit(`{"task":{"id":"synthetic","contextId":"synthetic","status":{"state":"TASK_STATE_SUBMITTED"}}}`) ||
			!emit(`{"statusUpdate":{`+ids+`,"status":{"state":"TASK_STATE_WORKING"}}}`) {
			return
		}
		timer := time.NewTimer(0)
		defer timer.Stop()
		<-timer.C
		for i, word := range words {
			if i > 0 {
				timer.Reset(delay)
				select {
				case <-timer.C:
				case <-r.Context().Done():
					return
				}
			}
			chunk, _ := json.Marshal(replyPrefix + word)
			if !emit(`{"artifactUpdate":{` + ids + `,"append":true,"artifact":{"artifactId":"a","parts":[{"text":` + string(chunk) + `}]}}}`) {
				return
			}
		}
		emit(`{"statusUpdate":{` + ids + `,"status":{"state":"TASK_STATE_COMPLETED"}}}`)
	})
}
