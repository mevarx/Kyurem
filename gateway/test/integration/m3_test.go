// M3 acceptance tests: the management API answers questions about the
// traffic the gateway actually proxied. The test drives real A2A calls
// through the real proxy handler and then reads the result back through
// the API the dashboard uses.
package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/mgmtapi"
	"github.com/kyurem-project/kyurem/gateway/internal/storage"
)

// mgmtGet calls the management API and decodes the JSON response.
func mgmtGet(t *testing.T, gw *gwStack, path string, out any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	gw.mgmt.ServeHTTP(rec, req)
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v (body %s)", path, err, rec.Body.String())
		}
	}
	return rec.Code
}

// waitForTasks polls the task list until it holds at least n tasks.
func waitForTasks(t *testing.T, gw *gwStack, n int) storage.TaskPage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var page storage.TaskPage
	for {
		if code := mgmtGet(t, gw, "/api/v1/tasks?limit=100", &page); code != http.StatusOK {
			t.Fatalf("GET /api/v1/tasks = %d", code)
		}
		if len(page.Items) >= n {
			return page
		}
		if time.Now().After(deadline) {
			t.Fatalf("tasks = %d, want %d", len(page.Items), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitForTaskSpans polls the task list until a task holds at least n spans.
// The spans reach the store asynchronously, so a task can be visible with
// only its first hop.
func waitForTaskSpans(t *testing.T, gw *gwStack, n int) storage.TaskPage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		page := waitForTasks(t, gw, 1)
		for _, task := range page.Items {
			if task.SpanCount >= n {
				return page
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no task with %d spans", n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// M3: a chained task is one task in the API, with the tree, the agents
// involved and the cost status (unknown when the agent reports no usage).
func TestManagementAPIReportsChainedTask(t *testing.T) {
	echo := startEchoAgent(t)
	upper := newAgentServer(t)
	gw := startGatewayWithExporter(t, agentEntries(echo.URL(), upper.URL()), nil)
	upper.setDelegate(upperAgentHandler(t, gw.URL()+"/a2a/echo"+cardSuffix))

	cancel, client := clientFor(t, gw.URL(), "upper")
	defer cancel()

	if _, err := sendText(context.Background(), client, "m3 task"); err != nil {
		t.Fatalf("send via upper: %v", err)
	}

	page := waitForTaskSpans(t, gw, 2)
	if len(page.Items) != 1 {
		t.Fatalf("tasks = %d, want 1", len(page.Items))
	}
	task := page.Items[0]
	if task.Status != "succeeded" {
		t.Errorf("task status = %q, want succeeded", task.Status)
	}
	if len(task.Agents) != 2 {
		t.Errorf("agents = %v, want upper and echo", task.Agents)
	}
	if task.SpanCount != 2 {
		t.Errorf("span count = %d, want 2", task.SpanCount)
	}
	if task.TokensKnown {
		t.Error("tokens_known = true, but the demo agent reports no usage")
	}
	if task.CostUSD != nil {
		t.Errorf("cost = %v, want unknown (nil) when no usage was reported", *task.CostUSD)
	}

	// The detail view carries the span tree and the (absent) root cause.
	var detail storage.TaskDetail
	if code := mgmtGet(t, gw, "/api/v1/tasks/"+task.TaskID, &detail); code != http.StatusOK {
		t.Fatalf("GET task detail = %d", code)
	}
	if len(detail.Spans) != 2 {
		t.Fatalf("detail spans = %d, want 2", len(detail.Spans))
	}
	if detail.RootCause != nil {
		t.Errorf("root cause = %+v, want none for a successful task", detail.RootCause)
	}
	var child, parent int
	for _, s := range detail.Spans {
		if s.ParentSpanID == "" {
			parent++
		} else {
			child++
		}
	}
	if parent != 1 || child != 1 {
		t.Errorf("tree shape = %d roots / %d children, want 1/1", parent, child)
	}

	// The graph connects the two agents, and the agent list knows them.
	var graph storage.Graph
	if code := mgmtGet(t, gw, "/api/v1/graph", &graph); code != http.StatusOK {
		t.Fatalf("GET graph = %d", code)
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("graph edges = %d, want 1 (upper -> echo)", len(graph.Edges))
	}
	if graph.Edges[0].From != "upper" || graph.Edges[0].To != "echo" {
		t.Errorf("edge = %s -> %s, want upper -> echo", graph.Edges[0].From, graph.Edges[0].To)
	}

	var agents struct {
		Items []struct {
			Name       string   `json:"name"`
			Spans      int      `json:"spans"`
			CardStatus string   `json:"card_status"`
			Aliases    []string `json:"aliases"`
		} `json:"items"`
	}
	if code := mgmtGet(t, gw, "/api/v1/agents", &agents); code != http.StatusOK {
		t.Fatalf("GET agents = %d", code)
	}
	if len(agents.Items) != 2 {
		t.Fatalf("agents = %d, want 2", len(agents.Items))
	}
	for _, a := range agents.Items {
		if a.Name == "echo" && a.Spans == 0 {
			t.Error("echo has no spans recorded")
		}
		if a.CardStatus == "" {
			t.Error("agent without a card status")
		}
	}

	// The overview counts the task.
	var overview storage.Overview
	if code := mgmtGet(t, gw, "/api/v1/overview", &overview); code != http.StatusOK {
		t.Fatalf("GET overview = %d", code)
	}
	if overview.TasksTotal != 1 {
		t.Errorf("tasks total = %d, want 1", overview.TasksTotal)
	}
	if overview.TasksSucceeded != 1 {
		t.Errorf("tasks succeeded = %d, want 1", overview.TasksSucceeded)
	}
}

// M3: a failed handoff is diagnosable - the task is failed, the detail
// names the failing span and the root-cause panel has a hint.
func TestManagementAPIDiagnosesFailure(t *testing.T) {
	// The upstream is unreachable: the gateway answers 502.
	gw := startGatewayWithExporter(t, agentEntries("http://127.0.0.1:1", "http://127.0.0.1:1"), nil)

	code, _, _ := postTrace(t, gw.URL()+"/a2a/echo", "", "", formatJSONRPCSendMessage("m3-fail", "nobody home"))
	if code != 502 {
		t.Fatalf("status = %d, want 502", code)
	}

	page := waitForTasks(t, gw, 1)
	task := page.Items[0]
	if task.Status != "failed" {
		t.Errorf("task status = %q, want failed", task.Status)
	}
	if task.ErrorType == "" {
		t.Error("failed task without an error type")
	}

	var detail storage.TaskDetail
	if code := mgmtGet(t, gw, "/api/v1/tasks/"+task.TaskID, &detail); code != http.StatusOK {
		t.Fatalf("GET task detail = %d", code)
	}
	if detail.RootCause == nil {
		t.Fatal("no root-cause hint for a failed task")
	}
	if detail.RootCause.Confidence != "heuristic" {
		t.Errorf("root cause confidence = %q, want heuristic", detail.RootCause.Confidence)
	}
	if detail.RootCause.Message == "" {
		t.Error("root cause without a message")
	}
	if detail.RootCause.SpanID == "" {
		t.Error("root cause without a span id")
	}

	// An unknown task is a 404, not an empty page.
	if code := mgmtGet(t, gw, "/api/v1/tasks/does-not-exist", nil); code != http.StatusNotFound {
		t.Errorf("GET unknown task = %d, want 404", code)
	}
}

// M3: the management API is authenticated when a token is configured.
func TestManagementAPIAuthentication(t *testing.T) {
	echo := startEchoAgent(t)
	gw := startGatewayWithExporter(t, agentEntries(echo.URL(), "http://127.0.0.1:1"), nil)

	// The harness runs the API without a token; build an authenticated one
	// on top of the same wiring.
	authed := authenticatedHandler(gw, "s3cret")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	authed.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without a token = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	authed.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("with a token = %d, want 200", rec.Code)
	}

	// The health probes stay open.
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec = httptest.NewRecorder()
	authed.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want 200", rec.Code)
	}
}

// M3: cost roll-ups keep unknown costs unknown.
func TestManagementAPICostIsUnknownWithoutUsage(t *testing.T) {
	echo := startEchoAgent(t)
	gw := startGatewayWithExporter(t, agentEntries(echo.URL(), "http://127.0.0.1:1"), nil)

	cancel, client := clientFor(t, gw.URL(), "echo")
	defer cancel()
	if _, err := sendText(context.Background(), client, "no usage here"); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitForTasks(t, gw, 1)

	var report storage.CostReport
	if code := mgmtGet(t, gw, "/api/v1/costs?group_by=agent", &report); code != http.StatusOK {
		t.Fatalf("GET costs = %d", code)
	}
	if report.TotalUSD != nil {
		t.Errorf("total cost = %v, want unknown (nil)", *report.TotalUSD)
	}
	if report.UnknownSpans == 0 {
		t.Error("unknown spans not counted")
	}
	for _, b := range report.Buckets {
		if b.CostUSD != nil {
			t.Errorf("bucket %q cost = %v, want unknown", b.Key, *b.CostUSD)
		}
	}
}

// M3: an agent that reports token usage produces a priced span (CO-1,
// CO-2). The cost is the price table applied to the reported counts.
func TestManagementAPIReportsCostWhenUsageIsReported(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{
			"id":"task-1","contextId":"ctx-1",
			"status":{"state":"TASK_STATE_COMPLETED"},
			"metadata":{"model":"demo-echo","usage":{"input_tokens":1000,"output_tokens":500}}}}`)
	}))
	defer stub.Close()

	gw := startGatewayWithExporter(t, agentEntries(stub.URL, "http://127.0.0.1:1"), nil)

	code, _, _ := postTrace(t, gw.URL()+"/a2a/echo", "", "", formatJSONRPCSendMessage("m3-cost", "priced"))
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}

	page := waitForTaskSpans(t, gw, 1)
	task := page.Items[0]
	if !task.TokensKnown {
		t.Fatal("tokens_known = false, but the stub reported usage")
	}
	if task.InputTokens == nil || *task.InputTokens != 1000 {
		t.Errorf("input tokens = %v, want 1000", task.InputTokens)
	}
	if task.OutputTokens == nil || *task.OutputTokens != 500 {
		t.Errorf("output tokens = %v, want 500", task.OutputTokens)
	}
	if task.CostUSD == nil {
		t.Fatal("cost unknown, but the usage and the price are known")
	}
	// 1000 input tokens at 0.001/1k plus 500 output tokens at 0.002/1k.
	want := 0.001 + 0.001
	if diff := *task.CostUSD - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("cost = %v, want %v", *task.CostUSD, want)
	}
}

// authenticatedHandler rebuilds the management handler with a token.
func authenticatedHandler(gw *gwStack, token string) http.Handler {
	return mgmtapi.New(mgmtapi.Deps{
		Queries:       gw.queries,
		Registry:      gw.reg,
		Metrics:       nil,
		StorageDriver: "memory",
		AuthToken:     token,
		Logger:        discardLogger(),
		Uptime:        time.Now(),
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
