// Package storage is the query layer behind the management API.
//
// Two things live here:
//
//   - Store: where finished spans come from. The M3 stack ships two
//     drivers: an in-process ring (memory) that needs nothing but the
//     gateway itself, and ClickHouse, the store the compose stack writes
//     to through the OTel collector.
//   - Queries: the aggregation semantics (tasks, span trees, agent graph,
//     cost roll-ups) implemented once on top of any Store, so both drivers
//     answer identically.
//
// The API surface is frozen: the management API, the dashboard and the
// tests all depend on these types.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

// ErrNotFound is returned by the query layer when a task id does not
// exist. The management API maps it to HTTP 404.
var ErrNotFound = errors.New("task not found")

// Window is a half-open time range [From, To). A zero From means "no
// lower bound", a zero To means "now".
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// TaskQuery filters and paginates the task list.
type TaskQuery struct {
	Window
	// Status filters on the derived task status: succeeded, failed or
	// unknown. Empty means all.
	Status string `json:"status"`
	// Agent filters on any agent involved (caller or callee).
	Agent string `json:"agent"`
	// TaskID is an exact task id or a prefix of one.
	TaskID string `json:"task_id"`
	// MinCostUSD and MaxCostUSD filter on the task cost when known.
	MinCostUSD *float64 `json:"min_cost_usd"`
	MaxCostUSD *float64 `json:"max_cost_usd"`
	// Limit and Offset paginate. Limit 0 means "server default".
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// Task is one A2A task as observed by the gateway: the root hop of a
// trace plus the aggregate of the hops below it.
type Task struct {
	TaskID    string `json:"task_id"`
	TraceID   string `json:"trace_id"`
	ContextID string `json:"context_id,omitempty"`
	// Status is succeeded, failed or unknown. Unknown is honest: the
	// gateway cannot know the outcome of a task it never saw finish.
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS float64   `json:"duration_ms"`
	// Agents lists every agent involved in the task, in first-seen order.
	Agents     []string `json:"agents"`
	RootCaller string   `json:"root_caller,omitempty"`
	// CostUSD is nil when the cost is unknown (agents that do not report
	// token usage). Unknown is never rendered as zero.
	CostUSD      *float64 `json:"cost_usd"`
	InputTokens  *int64   `json:"input_tokens"`
	OutputTokens *int64   `json:"output_tokens"`
	TokensKnown  bool     `json:"tokens_known"`
	SpanCount    int      `json:"span_count"`
	ErrorType    string   `json:"error_type,omitempty"`
	ErrorMessage string   `json:"error_message,omitempty"`
}

// TaskPage is a paginated task list.
type TaskPage struct {
	Items  []Task `json:"items"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// Span is a normalized span as served to the dashboard.
type Span struct {
	TraceID       string            `json:"trace_id"`
	SpanID        string            `json:"span_id"`
	ParentSpanID  string            `json:"parent_span_id"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	ServiceName   string            `json:"service_name"`
	Agent         string            `json:"agent"`
	Caller        string            `json:"caller"`
	ErrorType     string            `json:"error_type,omitempty"`
	StartedAt     time.Time         `json:"started_at"`
	DurationMS    float64           `json:"duration_ms"`
	StatusCode    string            `json:"status_code"`
	StatusMessage string            `json:"status_message,omitempty"`
	Attributes    map[string]string `json:"attributes"`
}

// RootCause is a heuristic hint about the earliest failure of a task. It
// is a hint, never a verdict (PRD §5.2).
type RootCause struct {
	SpanID    string `json:"span_id"`
	SpanName  string `json:"span_name"`
	Agent     string `json:"agent"`
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
	// AffectedSpanIDs lists the spans that ran after (or inside) the
	// failing span on the same trace.
	AffectedSpanIDs []string `json:"affected_span_ids"`
	Confidence      string   `json:"confidence"`
}

// CostBucket is one group of a cost roll-up.
type CostBucket struct {
	Key          string   `json:"key"`
	CostUSD      *float64 `json:"cost_usd"`
	Spans        int      `json:"spans"`
	UnknownSpans int      `json:"unknown_spans"`
	InputTokens  *int64   `json:"input_tokens"`
	OutputTokens *int64   `json:"output_tokens"`
}

// CostReport aggregates cost and token usage. A nil CostUSD means the
// cost is unknown for the whole group.
type CostReport struct {
	GroupBy      string       `json:"group_by"`
	TotalUSD     *float64     `json:"total_usd"`
	InputTokens  *int64       `json:"input_tokens"`
	OutputTokens *int64       `json:"output_tokens"`
	Spans        int          `json:"spans"`
	UnknownSpans int          `json:"unknown_spans"`
	Buckets      []CostBucket `json:"buckets"`
}

// CostQuery asks for a cost roll-up.
type CostQuery struct {
	Window
	// GroupBy is agent, model, task or window.
	GroupBy string `json:"group_by"`
}

// GraphNode is an agent in the call graph.
type GraphNode struct {
	Name      string     `json:"name"`
	Calls     int        `json:"calls"`
	Errors    int        `json:"errors"`
	ErrorRate float64    `json:"error_rate"`
	P50MS     float64    `json:"p50_ms"`
	P95MS     float64    `json:"p95_ms"`
	CostUSD   *float64   `json:"cost_usd"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

// GraphEdge is a caller-to-callee relationship.
type GraphEdge struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Calls     int      `json:"calls"`
	Errors    int      `json:"errors"`
	ErrorRate float64  `json:"error_rate"`
	P50MS     float64  `json:"p50_ms"`
	P95MS     float64  `json:"p95_ms"`
	CostUSD   *float64 `json:"cost_usd"`
}

// Graph is the agent-to-agent call graph for a window.
type Graph struct {
	Window
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// AgentStats are the span-derived statistics of one agent.
type AgentStats struct {
	Name         string     `json:"name"`
	Spans        int        `json:"spans"`
	Errors       int        `json:"errors"`
	ErrorRate    float64    `json:"error_rate"`
	P50MS        float64    `json:"p50_ms"`
	P95MS        float64    `json:"p95_ms"`
	CostUSD      *float64   `json:"cost_usd"`
	InputTokens  *int64     `json:"input_tokens"`
	OutputTokens *int64     `json:"output_tokens"`
	LastSeen     *time.Time `json:"last_seen,omitempty"`
}

// TimeBucket is one point of a time series.
type TimeBucket struct {
	Bucket    time.Time `json:"bucket"`
	Succeeded int       `json:"succeeded"`
	Failed    int       `json:"failed"`
	Unknown   int       `json:"unknown"`
	CostUSD   *float64  `json:"cost_usd"`
	Spans     int       `json:"spans"`
}

// FailingAgent ranks agents by failures.
type FailingAgent struct {
	Name      string  `json:"name"`
	Errors    int     `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
}

// Overview is the dashboard summary payload.
type Overview struct {
	Window
	TasksTotal      int            `json:"tasks_total"`
	TasksSucceeded  int            `json:"tasks_succeeded"`
	TasksFailed     int            `json:"tasks_failed"`
	ErrorRate       float64        `json:"error_rate"`
	P50TaskMS       float64        `json:"p50_task_duration_ms"`
	P95TaskMS       float64        `json:"p95_task_duration_ms"`
	TotalCostUSD    *float64       `json:"total_cost_usd"`
	ActiveAgents    int            `json:"active_agents"`
	SpansTotal      int            `json:"spans_total"`
	TasksOverTime   []TimeBucket   `json:"tasks_over_time"`
	CostOverTime    []TimeBucket   `json:"cost_over_time"`
	TopAgentsByCost []CostBucket   `json:"top_agents_by_cost"`
	TopFailing      []FailingAgent `json:"top_failing_agents"`
	RecentFailed    []Task         `json:"recent_failed_tasks"`
}

// Event is a span event (for example a recorded error).
type Event struct {
	SpanID     string            `json:"span_id"`
	Time       time.Time         `json:"time"`
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes"`
}

// SpanQuery selects raw spans. It is the only thing a Store has to
// implement: everything else is derived by Queries.
type SpanQuery struct {
	Window
	// TraceID restricts the query to a single trace.
	TraceID string
	// Limit caps the number of returned spans. Zero means no limit.
	Limit int
}

// Store is a source of finished spans.
type Store interface {
	// Spans returns the spans matching the query, oldest first.
	Spans(ctx context.Context, q SpanQuery) ([]telemetry.SpanRecord, error)
	// Close releases the resources held by the store.
	Close() error
}

// Queries is the aggregation layer used by the management API. It is
// implemented once (see query.go) and works against any Store.
type Queries interface {
	// ListTasks returns a filtered, paginated task list.
	ListTasks(ctx context.Context, q TaskQuery) (TaskPage, error)
	// TaskDetail returns the full picture of one task, including the span
	// tree, the root-cause hint and the cost breakdown.
	TaskDetail(ctx context.Context, taskID string) (*TaskDetail, error)
	// Graph returns the agent-to-agent call graph for a window.
	Graph(ctx context.Context, w Window) (*Graph, error)
	// Costs returns a cost and token roll-up.
	Costs(ctx context.Context, q CostQuery) (*CostReport, error)
	// AgentStats returns per-agent statistics for a window.
	AgentStats(ctx context.Context, w Window) ([]AgentStats, error)
	// Overview returns the dashboard summary for a window.
	Overview(ctx context.Context, w Window) (*Overview, error)
}

// TaskDetail is the task detail payload.
type TaskDetail struct {
	Task      Task       `json:"task"`
	Spans     []Span     `json:"spans"`
	RootCause *RootCause `json:"root_cause"`
	Cost      CostReport `json:"cost"`
	Events    []Event    `json:"events"`
}
