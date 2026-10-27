package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSyntheticStreamEmitsLifecycleAndFallsThrough(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	srv := httptest.NewServer(syntheticStream(fallback, time.Millisecond))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/", "application/json", strings.NewReader(
		`{"jsonrpc":"2.0","id":7,"method":"SendStreamingMessage","params":{"message":{"parts":[{"text":"x y"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var events []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") {
			events = append(events, sc.Text())
		}
	}
	if len(events) != 5 { // submitted, working, 2 words, completed
		t.Fatalf("got %d events, want 5: %v", len(events), events)
	}
	if !strings.Contains(events[0], `"id":7`) || !strings.Contains(events[4], "TASK_STATE_COMPLETED") {
		t.Fatalf("unexpected lifecycle: first=%s last=%s", events[0], events[4])
	}

	other, err := http.Post(srv.URL+"/", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"SendMessage"}`))
	if err != nil {
		t.Fatal(err)
	}
	other.Body.Close()
	if other.StatusCode != http.StatusTeapot {
		t.Fatalf("non-streaming call status %d, want fall-through", other.StatusCode)
	}
}
