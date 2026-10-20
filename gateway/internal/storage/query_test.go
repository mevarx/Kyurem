package storage

import (
	"context"
	"errors"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

// queryBase is the fixed start time of the test fixtures. Fixed timestamps
// keep the assertions deterministic.
var queryBase = time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)

// queryFakeStore is an in-memory Store: a slice of span records the test
// controls.
type queryFakeStore struct {
	spans []telemetry.SpanRecord
	err   error
}

// Spans returns the records matching the query, oldest first.
func (s *queryFakeStore) Spans(ctx context.Context, q SpanQuery) ([]telemetry.SpanRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]telemetry.SpanRecord, 0, len(s.spans))
	for _, rec := range s.spans {
		if q.TraceID != "" && rec.TraceID != q.TraceID {
			continue
		}
		if !q.From.IsZero() && rec.StartedAt.Before(q.From) {
			continue
		}
		if !q.To.IsZero() && !rec.StartedAt.Before(q.To) {
			continue
		}
		out = append(out, rec)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out, nil
}

// Close implements Store.
func (s *queryFakeStore) Close() error { return nil }

// querySpan builds a span record for the fixtures.
func querySpan(traceID, spanID, parent, name string, start time.Time, dur time.Duration, attrs map[string]string) telemetry.SpanRecord {
	if attrs == nil {
		attrs = map[string]string{}
	}
	return telemetry.SpanRecord{
		TraceID:      traceID,
		SpanID:       spanID,
		ParentSpanID: parent,
		Name:         name,
		Kind:         "server",
		ServiceName:  "kyurem-gateway",
		StartedAt:    start,
		EndedAt:      start.Add(dur),
		StatusCode:   "Unset",
		Attributes:   attrs,
		ResourceAttributes: map[string]string{
			"service.name": "kyurem-gateway",
		},
	}
}

// queryWindow returns a window that covers the fixtures.
func queryWindow() Window {
	return Window{From: queryBase.Add(-time.Hour), To: queryBase.Add(time.Hour)}
}

// queryTwoHopSpans is a successful upper -> echo trace: the root hop carries
// the task identity, the echo hop is its child.
func queryTwoHopSpans() []telemetry.SpanRecord {
	return []telemetry.SpanRecord{
		querySpan("trace-1", "span-1", "", "invoke_agent upper", queryBase, 120*time.Millisecond, map[string]string{
			telemetry.AttrTaskID:         "task-1",
			telemetry.AttrContextID:      "ctx-1",
			telemetry.AttrCallerAgent:    "unknown",
			telemetry.AttrCalleeAgent:    "upper",
			telemetry.AttrTaskStatus:     "TASK_STATE_COMPLETED",
			telemetry.AttrHTTPStatusCode: "200",
			telemetry.AttrTokensKnown:    "false",
		}),
		querySpan("trace-1", "span-2", "span-1", "invoke_agent echo", queryBase.Add(10*time.Millisecond), 90*time.Millisecond, map[string]string{
			telemetry.AttrContextID:   "ctx-1",
			telemetry.AttrCallerAgent: "upper",
			telemetry.AttrCalleeAgent: "echo",
			telemetry.AttrTokensKnown: "false",
		}),
	}
}

// queryFailedHopSpans is a trace whose echo hop failed, followed by a later
// hop that never ran.
func queryFailedHopSpans() []telemetry.SpanRecord {
	failed := querySpan("trace-2", "span-b", "span-a", "invoke_agent echo", queryBase.Add(10*time.Millisecond), 12300*time.Microsecond, map[string]string{
		telemetry.AttrCallerAgent: "upper",
		telemetry.AttrCalleeAgent: "echo",
		telemetry.AttrErrorType:   "upstream_error",
		telemetry.AttrTokensKnown: "false",
	})
	failed.StatusCode = "Error"
	failed.StatusMessage = "upstream unreachable"
	return []telemetry.SpanRecord{
		querySpan("trace-2", "span-a", "", "invoke_agent upper", queryBase, 50*time.Millisecond, map[string]string{
			telemetry.AttrTaskID:         "task-2",
			telemetry.AttrContextID:      "ctx-2",
			telemetry.AttrCallerAgent:    "unknown",
			telemetry.AttrCalleeAgent:    "upper",
			telemetry.AttrTaskStatus:     "TASK_STATE_COMPLETED",
			telemetry.AttrHTTPStatusCode: "200",
			telemetry.AttrTokensKnown:    "false",
		}),
		failed,
		querySpan("trace-2", "span-c", "span-a", "invoke_agent report", queryBase.Add(30*time.Millisecond), 5*time.Millisecond, map[string]string{
			telemetry.AttrCallerAgent: "upper",
			telemetry.AttrCalleeAgent: "report",
			telemetry.AttrTokensKnown: "false",
		}),
	}
}

// queryFilterSpans holds three tasks: a succeeded one with a known cost, a
// failed one without any cost and a succeeded one with a known cost.
func queryFilterSpans() []telemetry.SpanRecord {
	return []telemetry.SpanRecord{
		querySpan("trace-a", "a-root", "", "invoke_agent upper", queryBase, 200*time.Millisecond, map[string]string{
			telemetry.AttrTaskID:         "task-a",
			telemetry.AttrContextID:      "ctx-a",
			telemetry.AttrCallerAgent:    "unknown",
			telemetry.AttrCalleeAgent:    "upper",
			telemetry.AttrTaskStatus:     "TASK_STATE_COMPLETED",
			telemetry.AttrHTTPStatusCode: "200",
			attrCostUSD:                  "0.5",
			telemetry.AttrTokensKnown:    "true",
		}),
		querySpan("trace-a", "a-child", "a-root", "invoke_agent echo", queryBase.Add(10*time.Millisecond), 50*time.Millisecond, map[string]string{
			telemetry.AttrCallerAgent: "upper",
			telemetry.AttrCalleeAgent: "echo",
			attrCostUSD:               "0.0",
			telemetry.AttrTokensKnown: "true",
		}),
		querySpan("trace-b", "b-root", "", "invoke_agent upper", queryBase.Add(time.Minute), 30*time.Millisecond, map[string]string{
			telemetry.AttrTaskID:         "task-b",
			telemetry.AttrCallerAgent:    "unknown",
			telemetry.AttrCalleeAgent:    "upper",
			telemetry.AttrHTTPStatusCode: "500",
			telemetry.AttrErrorType:      "http_5xx",
			telemetry.AttrTokensKnown:    "false",
		}),
		querySpan("trace-c", "c-root", "", "invoke_agent echo", queryBase.Add(2*time.Minute), 40*time.Millisecond, map[string]string{
			telemetry.AttrTaskID:         "task-c",
			telemetry.AttrCallerAgent:    "client",
			telemetry.AttrCalleeAgent:    "echo",
			telemetry.AttrTaskStatus:     "TASK_STATE_COMPLETED",
			telemetry.AttrHTTPStatusCode: "200",
			attrCostUSD:                  "2.0",
			telemetry.AttrTokensKnown:    "true",
		}),
	}
}

// newQueryLayer builds the query layer over a fixture.
func newQueryLayer(spans []telemetry.SpanRecord) Queries {
	return NewQueries(&queryFakeStore{spans: spans})
}

// TestQueryListTasksTwoHopTrace checks the core grouping: one trace tree is
// one task, the root hop carries the identity and the agents are listed
// in first-seen order.
func TestQueryListTasksTwoHopTrace(t *testing.T) {
	q := newQueryLayer(queryTwoHopSpans())
	page, err := q.ListTasks(context.Background(), TaskQuery{Window: queryWindow()})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("want 1 task, got total=%d items=%d", page.Total, len(page.Items))
	}
	task := page.Items[0]
	if task.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want task-1", task.TaskID)
	}
	if task.TraceID != "trace-1" {
		t.Errorf("TraceID = %q, want trace-1", task.TraceID)
	}
	if task.ContextID != "ctx-1" {
		t.Errorf("ContextID = %q, want ctx-1", task.ContextID)
	}
	if task.Status != statusSucceeded {
		t.Errorf("Status = %q, want %q", task.Status, statusSucceeded)
	}
	if task.SpanCount != 2 {
		t.Errorf("SpanCount = %d, want 2", task.SpanCount)
	}
	if task.DurationMS != 120 {
		t.Errorf("DurationMS = %v, want 120", task.DurationMS)
	}
	if task.RootCaller != "unknown" {
		t.Errorf("RootCaller = %q, want unknown", task.RootCaller)
	}
	want := []string{"upper", "echo"}
	if len(task.Agents) != len(want) {
		t.Fatalf("Agents = %v, want %v", task.Agents, want)
	}
	for i, agent := range want {
		if task.Agents[i] != agent {
			t.Fatalf("Agents = %v, want %v", task.Agents, want)
		}
	}
	if task.TokensKnown {
		t.Error("TokensKnown = true, want false")
	}
	if task.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil (unknown)", *task.CostUSD)
	}
}

// TestQueryTaskDetailFailedHop checks that a failing hop fails the task and
// produces a root-cause hint with the affected spans.
func TestQueryTaskDetailFailedHop(t *testing.T) {
	q := newQueryLayer(queryFailedHopSpans())
	detail, err := q.TaskDetail(context.Background(), "task-2")
	if err != nil {
		t.Fatalf("TaskDetail: %v", err)
	}
	if detail.Task.Status != statusFailed {
		t.Errorf("Status = %q, want %q", detail.Task.Status, statusFailed)
	}
	if detail.Task.ErrorType != "upstream_error" {
		t.Errorf("ErrorType = %q, want upstream_error", detail.Task.ErrorType)
	}
	if detail.Task.ErrorMessage != "upstream unreachable" {
		t.Errorf("ErrorMessage = %q, want upstream unreachable", detail.Task.ErrorMessage)
	}
	if detail.RootCause == nil {
		t.Fatal("RootCause = nil, want a hint")
	}
	rc := detail.RootCause
	if rc.SpanID != "span-b" {
		t.Errorf("RootCause.SpanID = %q, want span-b", rc.SpanID)
	}
	if rc.SpanName != "invoke_agent echo" {
		t.Errorf("RootCause.SpanName = %q, want invoke_agent echo", rc.SpanName)
	}
	if rc.Agent != "echo" {
		t.Errorf("RootCause.Agent = %q, want echo", rc.Agent)
	}
	if rc.ErrorType != "upstream_error" {
		t.Errorf("RootCause.ErrorType = %q, want upstream_error", rc.ErrorType)
	}
	if rc.Confidence != rootCauseConfidence {
		t.Errorf("RootCause.Confidence = %q, want %q", rc.Confidence, rootCauseConfidence)
	}
	wantMessage := "invoke_agent echo failed after 12.3ms (error.type=upstream_error); 1 later spans are affected."
	if rc.Message != wantMessage {
		t.Errorf("RootCause.Message = %q, want %q", rc.Message, wantMessage)
	}
	if len(rc.AffectedSpanIDs) != 1 || rc.AffectedSpanIDs[0] != "span-c" {
		t.Errorf("AffectedSpanIDs = %v, want [span-c]", rc.AffectedSpanIDs)
	}
	if len(detail.Spans) != 3 {
		t.Fatalf("Spans = %d, want 3", len(detail.Spans))
	}
	if detail.Spans[0].SpanID != "span-a" || detail.Spans[2].SpanID != "span-c" {
		t.Errorf("Spans are not sorted by start time: %v", detail.Spans)
	}
	if detail.Spans[1].Agent != "echo" || detail.Spans[1].Caller != "upper" {
		t.Errorf("span attribution = agent %q caller %q, want echo/upper", detail.Spans[1].Agent, detail.Spans[1].Caller)
	}
	if detail.Spans[1].Attributes == nil {
		t.Error("span attributes = nil, want a copy")
	}
	if len(detail.Events) != 1 {
		t.Fatalf("Events = %d, want 1", len(detail.Events))
	}
	if detail.Events[0].Name != "exception" {
		t.Errorf("Event.Name = %q, want exception", detail.Events[0].Name)
	}
}

// TestQueryTaskDetailUnknownID checks the not-found path.
func TestQueryTaskDetailUnknownID(t *testing.T) {
	q := newQueryLayer(queryTwoHopSpans())
	detail, err := q.TaskDetail(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if detail != nil {
		t.Errorf("detail = %v, want nil", detail)
	}
}

// TestQueryCostUnknownAndKnown checks that an unreported cost stays unknown
// and that reported costs are summed.
func TestQueryCostUnknownAndKnown(t *testing.T) {
	unknown := querySpan("trace-u", "u-root", "", "invoke_agent echo", queryBase, 10*time.Millisecond, map[string]string{
		telemetry.AttrCalleeAgent: "echo",
	})
	known := querySpan("trace-k", "k-root", "", "invoke_agent echo", queryBase, 10*time.Millisecond, map[string]string{
		telemetry.AttrCalleeAgent: "echo",
		attrCostUSD:               "0.25",
	})
	known2 := querySpan("trace-k", "k-child", "k-root", "invoke_agent echo", queryBase.Add(time.Millisecond), 10*time.Millisecond, map[string]string{
		telemetry.AttrCalleeAgent: "echo",
		attrCostUSD:               "0.75",
		attrUsageInput:            "10",
		attrUsageOutput:           "4",
	})
	tests := []struct {
		name       string
		spans      []telemetry.SpanRecord
		wantCost   *float64
		wantInput  *int64
		wantOutput *int64
	}{
		{name: "unknown", spans: []telemetry.SpanRecord{unknown}},
		{
			name:       "known",
			spans:      []telemetry.SpanRecord{known, known2},
			wantCost:   queryPtr(1.0),
			wantInput:  queryPtr(int64(10)),
			wantOutput: queryPtr(int64(4)),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := newQueryLayer(tc.spans)
			taskID := "trace-u"
			if tc.wantCost != nil {
				taskID = "trace-k"
			}
			detail, err := q.TaskDetail(context.Background(), taskID)
			if err != nil {
				t.Fatalf("TaskDetail: %v", err)
			}
			checkQueryFloat(t, "task cost", detail.Task.CostUSD, tc.wantCost)
			checkQueryInt(t, "task input tokens", detail.Task.InputTokens, tc.wantInput)
			checkQueryInt(t, "task output tokens", detail.Task.OutputTokens, tc.wantOutput)
			checkQueryFloat(t, "report total", detail.Cost.TotalUSD, tc.wantCost)
			if detail.Cost.Spans != len(tc.spans) {
				t.Errorf("cost report spans = %d, want %d", detail.Cost.Spans, len(tc.spans))
			}
			if len(detail.Cost.Buckets) != 1 {
				t.Fatalf("cost buckets = %d, want 1", len(detail.Cost.Buckets))
			}
			checkQueryFloat(t, "bucket cost", detail.Cost.Buckets[0].CostUSD, tc.wantCost)
		})
	}
}

// TestQueryListTasksFiltersAndPagination checks the filters and the
// pagination of the task list.
func TestQueryListTasksFiltersAndPagination(t *testing.T) {
	q := newQueryLayer(queryFilterSpans())
	tests := []struct {
		name      string
		query     TaskQuery
		wantTotal int
		wantIDs   []string
	}{
		{
			name:      "all",
			query:     TaskQuery{Window: queryWindow()},
			wantTotal: 3,
			wantIDs:   []string{"task-c", "task-b", "task-a"},
		},
		{
			name:      "status failed",
			query:     TaskQuery{Window: queryWindow(), Status: statusFailed},
			wantTotal: 1,
			wantIDs:   []string{"task-b"},
		},
		{
			name:      "status succeeded",
			query:     TaskQuery{Window: queryWindow(), Status: statusSucceeded},
			wantTotal: 2,
			wantIDs:   []string{"task-c", "task-a"},
		},
		{
			name:      "agent callee",
			query:     TaskQuery{Window: queryWindow(), Agent: "echo"},
			wantTotal: 2,
			wantIDs:   []string{"task-c", "task-a"},
		},
		{
			name:      "agent is case insensitive",
			query:     TaskQuery{Window: queryWindow(), Agent: "ECHO"},
			wantTotal: 2,
			wantIDs:   []string{"task-c", "task-a"},
		},
		{
			name:      "agent is the root caller",
			query:     TaskQuery{Window: queryWindow(), Agent: "client"},
			wantTotal: 1,
			wantIDs:   []string{"task-c"},
		},
		{
			name:      "task id prefix",
			query:     TaskQuery{Window: queryWindow(), TaskID: "task-"},
			wantTotal: 3,
			wantIDs:   []string{"task-c", "task-b", "task-a"},
		},
		{
			name:      "task id prefix is case insensitive",
			query:     TaskQuery{Window: queryWindow(), TaskID: "TASK-B"},
			wantTotal: 1,
			wantIDs:   []string{"task-b"},
		},
		{
			name:      "min cost excludes unknown cost",
			query:     TaskQuery{Window: queryWindow(), MinCostUSD: queryPtr(1.0)},
			wantTotal: 1,
			wantIDs:   []string{"task-c"},
		},
		{
			name:      "max cost excludes unknown cost",
			query:     TaskQuery{Window: queryWindow(), MaxCostUSD: queryPtr(1.0)},
			wantTotal: 1,
			wantIDs:   []string{"task-a"},
		},
		{
			name:      "cost range",
			query:     TaskQuery{Window: queryWindow(), MinCostUSD: queryPtr(0.5), MaxCostUSD: queryPtr(1.0)},
			wantTotal: 1,
			wantIDs:   []string{"task-a"},
		},
		{
			name:      "limit",
			query:     TaskQuery{Window: queryWindow(), Limit: 2},
			wantTotal: 3,
			wantIDs:   []string{"task-c", "task-b"},
		},
		{
			name:      "offset",
			query:     TaskQuery{Window: queryWindow(), Limit: 2, Offset: 2},
			wantTotal: 3,
			wantIDs:   []string{"task-a"},
		},
		{
			name:      "offset past the end",
			query:     TaskQuery{Window: queryWindow(), Limit: 2, Offset: 9},
			wantTotal: 3,
			wantIDs:   []string{},
		},
		{
			name:      "limit is clamped",
			query:     TaskQuery{Window: queryWindow(), Limit: 5000},
			wantTotal: 3,
			wantIDs:   []string{"task-c", "task-b", "task-a"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := q.ListTasks(context.Background(), tc.query)
			if err != nil {
				t.Fatalf("ListTasks: %v", err)
			}
			if page.Total != tc.wantTotal {
				t.Errorf("Total = %d, want %d", page.Total, tc.wantTotal)
			}
			if page.Items == nil {
				t.Fatal("Items = nil, want an empty slice")
			}
			if len(page.Items) != len(tc.wantIDs) {
				t.Fatalf("Items = %v, want %v", page.Items, tc.wantIDs)
			}
			for i, id := range tc.wantIDs {
				if page.Items[i].TaskID != id {
					t.Errorf("Items[%d].TaskID = %q, want %q", i, page.Items[i].TaskID, id)
				}
			}
		})
	}
}

// TestQueryListTasksEmptyResult checks that an empty page is not nil.
func TestQueryListTasksEmptyResult(t *testing.T) {
	q := newQueryLayer(queryTwoHopSpans())
	page, err := q.ListTasks(context.Background(), TaskQuery{Window: queryWindow(), Status: "failed"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if page.Total != 0 {
		t.Errorf("Total = %d, want 0", page.Total)
	}
	if page.Items == nil {
		t.Fatal("Items = nil, want an empty slice")
	}
	if len(page.Items) != 0 {
		t.Errorf("Items = %v, want empty", page.Items)
	}
	if page.Limit != defaultLimit {
		t.Errorf("Limit = %d, want %d", page.Limit, defaultLimit)
	}
}

// TestQueryGraphEdges checks that only real agent-to-agent edges are drawn.
func TestQueryGraphEdges(t *testing.T) {
	failed := querySpan("trace-g", "g-child", "g-root", "invoke_agent echo", queryBase.Add(10*time.Millisecond), 12300*time.Microsecond, map[string]string{
		telemetry.AttrCallerAgent: "upper",
		telemetry.AttrCalleeAgent: "echo",
		telemetry.AttrErrorType:   "upstream_error",
	})
	failed.StatusCode = "Error"
	spans := []telemetry.SpanRecord{
		querySpan("trace-g", "g-root", "", "invoke_agent upper", queryBase, 100*time.Millisecond, map[string]string{
			telemetry.AttrCallerAgent: "unknown",
			telemetry.AttrCalleeAgent: "upper",
		}),
		failed,
		querySpan("trace-g", "g-orphan", "", "invoke_agent echo", queryBase.Add(20*time.Millisecond), 100*time.Millisecond, map[string]string{
			telemetry.AttrCallerAgent: "unknown",
			telemetry.AttrCalleeAgent: "echo",
		}),
	}
	q := newQueryLayer(spans)
	graph, err := q.Graph(context.Background(), queryWindow())
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("Edges = %+v, want a single edge", graph.Edges)
	}
	edge := graph.Edges[0]
	if edge.From != "upper" || edge.To != "echo" {
		t.Errorf("edge = %s -> %s, want upper -> echo", edge.From, edge.To)
	}
	if edge.Calls != 1 || edge.Errors != 1 || edge.ErrorRate != 1 {
		t.Errorf("edge calls=%d errors=%d rate=%v, want 1/1/1", edge.Calls, edge.Errors, edge.ErrorRate)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("Nodes = %+v, want 2 nodes", graph.Nodes)
	}
	if graph.Nodes[0].Name != "echo" || graph.Nodes[1].Name != "upper" {
		t.Errorf("Nodes = %v, want sorted by name", graph.Nodes)
	}
	echo := graph.Nodes[0]
	if echo.Calls != 2 || echo.Errors != 1 || echo.ErrorRate != 0.5 {
		t.Errorf("echo node = calls %d errors %d rate %v, want 2/1/0.5", echo.Calls, echo.Errors, echo.ErrorRate)
	}
	if echo.P50MS != 12.3 || echo.P95MS != 100 {
		t.Errorf("echo percentiles = %v/%v, want 12.3/100", echo.P50MS, echo.P95MS)
	}
	if echo.CostUSD != nil {
		t.Errorf("echo cost = %v, want nil", *echo.CostUSD)
	}
	if echo.LastSeen == nil {
		t.Error("echo last seen = nil, want a timestamp")
	}
}

// TestQueryCostsGrouping checks the agent and model roll-ups.
func TestQueryCostsGrouping(t *testing.T) {
	spans := []telemetry.SpanRecord{
		querySpan("trace-1", "c-1", "", "invoke_agent upper", queryBase, 10*time.Millisecond, map[string]string{
			telemetry.AttrCalleeAgent: "upper",
			attrCostUSD:               "0.25",
			attrRequestModel:          "model-a",
			attrUsageInput:            "5",
			attrUsageOutput:           "2",
			telemetry.AttrTokensKnown: "true",
		}),
		querySpan("trace-2", "c-2", "", "invoke_agent echo", queryBase.Add(time.Minute), 10*time.Millisecond, map[string]string{
			telemetry.AttrCalleeAgent: "echo",
			attrCostUSD:               "0.75",
			attrRequestModel:          "model-a",
			telemetry.AttrTokensKnown: "false",
		}),
		querySpan("trace-3", "c-3", "", "invoke_agent echo", queryBase.Add(2*time.Minute), 10*time.Millisecond, map[string]string{
			telemetry.AttrCalleeAgent: "echo",
			telemetry.AttrTokensKnown: "false",
		}),
		querySpan("trace-4", "c-4", "", "invoke_agent echo", queryBase.Add(90*time.Minute), 10*time.Millisecond, map[string]string{
			telemetry.AttrCalleeAgent: "echo",
			attrRequestModel:          "model-b",
			telemetry.AttrTokensKnown: "false",
		}),
	}
	costWindow := Window{From: queryBase.Add(-time.Hour), To: queryBase.Add(3 * time.Hour)}
	q := newQueryLayer(spans)
	t.Run("by agent", func(t *testing.T) {
		report, err := q.Costs(context.Background(), CostQuery{Window: costWindow, GroupBy: groupByAgent})
		if err != nil {
			t.Fatalf("Costs: %v", err)
		}
		if report.GroupBy != groupByAgent {
			t.Errorf("GroupBy = %q, want %q", report.GroupBy, groupByAgent)
		}
		if report.Spans != 4 {
			t.Errorf("Spans = %d, want 4", report.Spans)
		}
		if report.UnknownSpans != 3 {
			t.Errorf("UnknownSpans = %d, want 3", report.UnknownSpans)
		}
		checkQueryFloat(t, "total", report.TotalUSD, queryPtr(1.0))
		checkQueryInt(t, "input tokens", report.InputTokens, queryPtr(int64(5)))
		checkQueryInt(t, "output tokens", report.OutputTokens, queryPtr(int64(2)))
		if len(report.Buckets) != 2 {
			t.Fatalf("Buckets = %+v, want 2", report.Buckets)
		}
		if report.Buckets[0].Key != "echo" || report.Buckets[1].Key != "upper" {
			t.Fatalf("Buckets = %+v, want sorted keys", report.Buckets)
		}
		checkQueryFloat(t, "echo cost", report.Buckets[0].CostUSD, queryPtr(0.75))
		if report.Buckets[0].Spans != 3 {
			t.Errorf("echo spans = %d, want 3", report.Buckets[0].Spans)
		}
		checkQueryFloat(t, "upper cost", report.Buckets[1].CostUSD, queryPtr(0.25))
	})
	t.Run("by model", func(t *testing.T) {
		report, err := q.Costs(context.Background(), CostQuery{Window: costWindow, GroupBy: groupByModel})
		if err != nil {
			t.Fatalf("Costs: %v", err)
		}
		if len(report.Buckets) != 3 {
			t.Fatalf("Buckets = %+v, want 3", report.Buckets)
		}
		keys := []string{report.Buckets[0].Key, report.Buckets[1].Key, report.Buckets[2].Key}
		want := []string{"model-a", "model-b", unknownBucketKey}
		for i := range want {
			if keys[i] != want[i] {
				t.Fatalf("bucket keys = %v, want %v", keys, want)
			}
		}
		checkQueryFloat(t, "model-a cost", report.Buckets[0].CostUSD, queryPtr(1.0))
		checkQueryFloat(t, "model-b cost", report.Buckets[1].CostUSD, nil)
		checkQueryFloat(t, "unknown cost", report.Buckets[2].CostUSD, nil)
	})
	t.Run("by task", func(t *testing.T) {
		report, err := q.Costs(context.Background(), CostQuery{Window: costWindow, GroupBy: groupByTask})
		if err != nil {
			t.Fatalf("Costs: %v", err)
		}
		if len(report.Buckets) != 4 {
			t.Fatalf("Buckets = %+v, want 4", report.Buckets)
		}
		if report.Buckets[0].Key != "trace-1" {
			t.Errorf("bucket key = %q, want trace-1", report.Buckets[0].Key)
		}
	})
	t.Run("by window", func(t *testing.T) {
		report, err := q.Costs(context.Background(), CostQuery{Window: costWindow, GroupBy: groupByWindow})
		if err != nil {
			t.Fatalf("Costs: %v", err)
		}
		if len(report.Buckets) != 2 {
			t.Fatalf("Buckets = %+v, want 2 hour buckets", report.Buckets)
		}
		if report.Buckets[0].Key != queryBase.Format(time.RFC3339) {
			t.Errorf("bucket key = %q, want %q", report.Buckets[0].Key, queryBase.Format(time.RFC3339))
		}
		if report.Buckets[0].Spans != 3 || report.Buckets[1].Spans != 1 {
			t.Errorf("bucket spans = %d/%d, want 3/1", report.Buckets[0].Spans, report.Buckets[1].Spans)
		}
	})
	t.Run("defaults to agent", func(t *testing.T) {
		report, err := q.Costs(context.Background(), CostQuery{Window: costWindow})
		if err != nil {
			t.Fatalf("Costs: %v", err)
		}
		if report.GroupBy != groupByAgent {
			t.Errorf("GroupBy = %q, want %q", report.GroupBy, groupByAgent)
		}
	})
}

// TestQueryOverview checks the counters and the time series of the dashboard
// summary.
func TestQueryOverview(t *testing.T) {
	q := newQueryLayer(queryFilterSpans())
	overview, err := q.Overview(context.Background(), Window{From: queryBase, To: queryBase.Add(30 * time.Minute)})
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if overview.TasksTotal != 3 {
		t.Errorf("TasksTotal = %d, want 3", overview.TasksTotal)
	}
	if overview.TasksSucceeded != 2 {
		t.Errorf("TasksSucceeded = %d, want 2", overview.TasksSucceeded)
	}
	if overview.TasksFailed != 1 {
		t.Errorf("TasksFailed = %d, want 1", overview.TasksFailed)
	}
	if math.Abs(overview.ErrorRate-1.0/3.0) > 1e-9 {
		t.Errorf("ErrorRate = %v, want %v", overview.ErrorRate, 1.0/3.0)
	}
	if overview.SpansTotal != 4 {
		t.Errorf("SpansTotal = %d, want 4", overview.SpansTotal)
	}
	if overview.ActiveAgents != 2 {
		t.Errorf("ActiveAgents = %d, want 2", overview.ActiveAgents)
	}
	checkQueryFloat(t, "total cost", overview.TotalCostUSD, queryPtr(2.5))
	if overview.P50TaskMS != 40 {
		t.Errorf("P50TaskMS = %v, want 40", overview.P50TaskMS)
	}
	if len(overview.TasksOverTime) != 30 {
		t.Fatalf("TasksOverTime = %d buckets, want 30 (one minute)", len(overview.TasksOverTime))
	}
	if len(overview.CostOverTime) != 30 {
		t.Fatalf("CostOverTime = %d buckets, want 30 (one minute)", len(overview.CostOverTime))
	}
	succeeded, failed := 0, 0
	for _, bucket := range overview.TasksOverTime {
		succeeded += bucket.Succeeded
		failed += bucket.Failed
	}
	if succeeded != 2 || failed != 1 {
		t.Errorf("task buckets = %d succeeded / %d failed, want 2/1", succeeded, failed)
	}
	if overview.TasksOverTime[0].Bucket != queryBase {
		t.Errorf("first bucket = %v, want %v", overview.TasksOverTime[0].Bucket, queryBase)
	}
	spans := 0
	for _, bucket := range overview.CostOverTime {
		spans += bucket.Spans
	}
	if spans != 4 {
		t.Errorf("cost buckets hold %d spans, want 4", spans)
	}
	if len(overview.TopFailing) != 1 || overview.TopFailing[0].Name != "upper" {
		t.Errorf("TopFailing = %+v, want upper", overview.TopFailing)
	}
	if len(overview.RecentFailed) != 1 || overview.RecentFailed[0].TaskID != "task-b" {
		t.Errorf("RecentFailed = %+v, want task-b", overview.RecentFailed)
	}
	if len(overview.TopAgentsByCost) == 0 {
		t.Fatal("TopAgentsByCost is empty")
	}
	if overview.TopAgentsByCost[0].Key != "echo" {
		t.Errorf("TopAgentsByCost[0] = %q, want echo", overview.TopAgentsByCost[0].Key)
	}
}

// TestQueryOverviewSlicesAreNotNil checks that the dashboard never receives a
// null slice.
func TestQueryOverviewSlicesAreNotNil(t *testing.T) {
	q := newQueryLayer(nil)
	overview, err := q.Overview(context.Background(), queryWindow())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if overview.TasksOverTime == nil || overview.CostOverTime == nil ||
		overview.TopAgentsByCost == nil || overview.TopFailing == nil || overview.RecentFailed == nil {
		t.Error("overview slices must not be nil")
	}
}

// TestQueryAgentStats checks the per-agent statistics.
func TestQueryAgentStats(t *testing.T) {
	q := newQueryLayer(queryFilterSpans())
	stats, err := q.AgentStats(context.Background(), queryWindow())
	if err != nil {
		t.Fatalf("AgentStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want 2 agents", stats)
	}
	if stats[0].Name != "echo" || stats[1].Name != "upper" {
		t.Fatalf("stats = %+v, want sorted by name", stats)
	}
	echo := stats[0]
	if echo.Spans != 2 || echo.Errors != 0 || echo.ErrorRate != 0 {
		t.Errorf("echo stats = %+v, want 2 spans and no errors", echo)
	}
	checkQueryFloat(t, "echo cost", echo.CostUSD, queryPtr(2.0))
	checkQueryInt(t, "echo input tokens", echo.InputTokens, nil)
	upper := stats[1]
	if upper.Spans != 2 || upper.Errors != 1 {
		t.Errorf("upper stats = %+v, want 2 spans and 1 error", upper)
	}
}

// TestQueryMalformedAttributes checks that broken attribute values are
// ignored instead of breaking the aggregation.
func TestQueryMalformedAttributes(t *testing.T) {
	spans := []telemetry.SpanRecord{
		querySpan("trace-x", "x-root", "", "invoke_agent echo", queryBase, 10*time.Millisecond, map[string]string{
			telemetry.AttrCalleeAgent:    "echo",
			attrCostUSD:                  "not-a-number",
			attrUsageInput:               "1.5.2",
			telemetry.AttrHTTPStatusCode: "not-a-number",
		}),
	}
	q := newQueryLayer(spans)
	page, err := q.ListTasks(context.Background(), TaskQuery{Window: queryWindow()})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("Total = %d, want 1", page.Total)
	}
	task := page.Items[0]
	if task.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil", *task.CostUSD)
	}
	if task.InputTokens != nil {
		t.Errorf("InputTokens = %v, want nil", *task.InputTokens)
	}
	if task.Status != statusUnknown {
		t.Errorf("Status = %q, want %q", task.Status, statusUnknown)
	}
}

// TestQueryContextCancellation checks that a cancelled context stops the
// query.
func TestQueryContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := newQueryLayer(queryTwoHopSpans())
	if _, err := q.ListTasks(ctx, TaskQuery{Window: queryWindow()}); !errors.Is(err, context.Canceled) {
		t.Errorf("ListTasks err = %v, want context.Canceled", err)
	}
	if _, err := q.Graph(ctx, queryWindow()); !errors.Is(err, context.Canceled) {
		t.Errorf("Graph err = %v, want context.Canceled", err)
	}
	if _, err := q.Costs(ctx, CostQuery{Window: queryWindow()}); !errors.Is(err, context.Canceled) {
		t.Errorf("Costs err = %v, want context.Canceled", err)
	}
	if _, err := q.AgentStats(ctx, queryWindow()); !errors.Is(err, context.Canceled) {
		t.Errorf("AgentStats err = %v, want context.Canceled", err)
	}
	if _, err := q.Overview(ctx, queryWindow()); !errors.Is(err, context.Canceled) {
		t.Errorf("Overview err = %v, want context.Canceled", err)
	}
}

// TestQueryStoreError checks that a failing store is reported.
func TestQueryStoreError(t *testing.T) {
	q := NewQueries(&queryFakeStore{err: errors.New("boom")})
	if _, err := q.ListTasks(context.Background(), TaskQuery{}); err == nil {
		t.Error("ListTasks err = nil, want the store error")
	}
}

// TestQueryPercentile checks the nearest-rank percentile.
func TestQueryPercentile(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		q      float64
		want   float64
	}{
		{name: "empty", values: nil, q: 0.5, want: 0},
		{name: "single", values: []float64{7}, q: 0.95, want: 7},
		{name: "p50 of two", values: []float64{2, 1}, q: 0.5, want: 1},
		{name: "p95 of two", values: []float64{2, 1}, q: 0.95, want: 2},
		{name: "p50 of four", values: []float64{4, 1, 3, 2}, q: 0.5, want: 2},
		{name: "p95 of twenty", values: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, q: 0.95, want: 19},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.values, tc.q); got != tc.want {
				t.Errorf("percentile(%v, %v) = %v, want %v", tc.values, tc.q, got, tc.want)
			}
		})
	}
}

// queryPtr returns a pointer to a copy of the value.
func queryPtr[T any](v T) *T {
	return &v
}

// checkQueryFloat compares an optional float.
func checkQueryFloat(t *testing.T, name string, got, want *float64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s = %v, want nil", name, *got)
		}
		return
	}
	if got == nil {
		t.Errorf("%s = nil, want %v", name, *want)
		return
	}
	if math.Abs(*got-*want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}

// checkQueryInt compares an optional integer.
func checkQueryInt(t *testing.T, name string, got, want *int64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s = %v, want nil", name, *got)
		}
		return
	}
	if got == nil {
		t.Errorf("%s = nil, want %v", name, *want)
		return
	}
	if *got != *want {
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}
