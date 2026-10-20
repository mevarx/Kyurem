// Package storage aggregates the spans a Store returns into the views the
// management API serves: tasks, task details, the agent graph, cost
// roll-ups, per-agent statistics and the dashboard overview.
//
// The grouping rule is the trace: one A2A task is one trace tree, so a
// trace id identifies a task and the root span of the tree carries the
// task identity (task id, context id, caller). Everything else is derived
// from the spans of that tree.
//
// The layer never invents data. An unknown cost stays nil (never zero), a
// task the gateway never saw finish stays "unknown" and the root cause is
// always labeled as a heuristic. Malformed attribute values are ignored
// rather than fatal: a broken span must not break the dashboard.
package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
)

// Attribute names the query layer reads. They alias the constants of the
// telemetry package, which owns the attribute vocabulary: the query layer
// never invents a name of its own.
const (
	attrCostUSD      = telemetry.AttrCostUSD
	attrUsageInput   = telemetry.AttrUsageInput
	attrUsageOutput  = telemetry.AttrUsageOutput
	attrRequestModel = telemetry.AttrRequestModel
)

// Derived task statuses.
const (
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusUnknown   = "unknown"
)

// Grouping keys of a cost roll-up.
const (
	groupByAgent  = "agent"
	groupByModel  = "model"
	groupByTask   = "task"
	groupByWindow = "window"
)

// rootCauseConfidence labels the root cause as a heuristic hint, never a
// verdict (PRD §5.2).
const rootCauseConfidence = "heuristic"

// unknownBucketKey is the bucket key of a span that reports no model.
const unknownBucketKey = "unknown"

// unknownErrorType names a failure that carries no error.type attribute.
const unknownErrorType = "unknown"

// Pagination and window defaults.
const (
	defaultLimit  = 50
	maxLimit      = 500
	defaultWindow = 24 * time.Hour
)

// errNoStore is returned when the query layer is used without a store.
var errNoStore = errors.New("storage: no store configured")

// queries implements Queries on top of a Store.
type queries struct {
	store Store
}

// queries implements the frozen Queries interface.
var _ Queries = (*queries)(nil)

// NewQueries returns the aggregation layer for a store.
//
// The layer never owns the store: closing the store stays the caller's
// job. The returned Queries is safe for concurrent use as long as the
// store is.
func NewQueries(s Store) Queries {
	return &queries{store: s}
}

// ---------------------------------------------------------------------------
// span groups: one trace tree is one task
// ---------------------------------------------------------------------------

// spanGroup is one trace tree: the spans that share a trace id.
type spanGroup struct {
	traceID string
	// spans holds the group sorted by StartedAt ascending.
	spans []telemetry.SpanRecord
	// root is the span that roots the tree inside the group.
	root telemetry.SpanRecord
}

// buildGroups groups spans by trace id and resolves the root span of
// every group.
func buildGroups(spans []telemetry.SpanRecord) []*spanGroup {
	byTrace := make(map[string][]telemetry.SpanRecord)
	order := make([]string, 0, len(byTrace))
	for _, s := range spans {
		if _, seen := byTrace[s.TraceID]; !seen {
			order = append(order, s.TraceID)
		}
		byTrace[s.TraceID] = append(byTrace[s.TraceID], s)
	}
	groups := make([]*spanGroup, 0, len(order))
	for _, id := range order {
		members := byTrace[id]
		sort.SliceStable(members, func(i, j int) bool {
			return members[i].StartedAt.Before(members[j].StartedAt)
		})
		groups = append(groups, &spanGroup{
			traceID: id,
			spans:   members,
			root:    findRoot(members),
		})
	}
	return groups
}

// findRoot returns the root span of a group: the span without a parent or
// the span whose parent is not part of the group. When several qualify,
// the earliest one wins.
func findRoot(spans []telemetry.SpanRecord) telemetry.SpanRecord {
	if len(spans) == 0 {
		return telemetry.SpanRecord{}
	}
	ids := make(map[string]struct{}, len(spans))
	for _, s := range spans {
		ids[s.SpanID] = struct{}{}
	}
	for _, s := range spans {
		if s.ParentSpanID == "" {
			return s
		}
		if _, ok := ids[s.ParentSpanID]; !ok {
			return s
		}
	}
	// Every span has a parent inside the group (a cycle): fall back to
	// the earliest span instead of dropping the group.
	return spans[0]
}

// ---------------------------------------------------------------------------
// task derivation
// ---------------------------------------------------------------------------

// deriveTask summarizes one trace tree as a task.
func deriveTask(g *spanGroup) Task {
	root := g.root
	task := Task{
		TaskID:     g.traceID,
		TraceID:    g.traceID,
		ContextID:  root.Attributes[telemetry.AttrContextID],
		StartedAt:  root.StartedAt,
		DurationMS: durationMS(root.Duration()),
		SpanCount:  len(g.spans),
		Agents:     []string{},
	}
	if id := root.Attributes[telemetry.AttrTaskID]; id != "" {
		task.TaskID = id
	}
	task.RootCaller = root.Attributes[telemetry.AttrCallerAgent]
	task.Status = deriveStatus(g)
	task.Agents = collectAgents(g.spans)
	if fail := earliestFailure(g.spans); fail != nil {
		task.ErrorType = fail.Attributes[telemetry.AttrErrorType]
		task.ErrorMessage = fail.StatusMessage
	}
	acc := &costAccumulator{}
	for _, s := range g.spans {
		acc.add(s)
	}
	task.CostUSD = acc.costUSD()
	task.InputTokens = acc.inputTokens()
	task.OutputTokens = acc.outputTokens()
	task.TokensKnown = acc.known > 0
	return task
}

// deriveStatus derives the task status from the spans of a group.
//
// A group with any failing span is failed. Otherwise the root span
// decides: a completed A2A task or a non-error HTTP status means
// succeeded. Everything else stays unknown: the gateway cannot know the
// outcome of a task it never saw finish.
func deriveStatus(g *spanGroup) string {
	for _, s := range g.spans {
		if isFailing(s) {
			return statusFailed
		}
	}
	root := g.root
	if strings.Contains(strings.ToUpper(root.Attributes[telemetry.AttrTaskStatus]), "COMPLETED") {
		return statusSucceeded
	}
	if code, ok := parseIntAttr(root, telemetry.AttrHTTPStatusCode); ok && code >= 200 && code <= 399 {
		return statusSucceeded
	}
	return statusUnknown
}

// collectAgents returns the distinct callee agents of a group in
// first-seen order (by span start time).
func collectAgents(spans []telemetry.SpanRecord) []string {
	agents := make([]string, 0, len(spans))
	seen := make(map[string]struct{}, len(spans))
	for _, s := range spans {
		agent := s.Attributes[telemetry.AttrCalleeAgent]
		if agent == "" {
			continue
		}
		if _, dup := seen[agent]; dup {
			continue
		}
		seen[agent] = struct{}{}
		agents = append(agents, agent)
	}
	return agents
}

// earliestFailure returns the first failing span of a sorted span list.
func earliestFailure(spans []telemetry.SpanRecord) *telemetry.SpanRecord {
	for i := range spans {
		if isFailing(spans[i]) {
			return &spans[i]
		}
	}
	return nil
}

// isFailing reports whether a span carries a failure: an error.type
// attribute or an error span status.
func isFailing(s telemetry.SpanRecord) bool {
	if strings.TrimSpace(s.Attributes[telemetry.AttrErrorType]) != "" {
		return true
	}
	return strings.EqualFold(s.StatusCode, "Error")
}

// ---------------------------------------------------------------------------
// ListTasks
// ---------------------------------------------------------------------------

// ListTasks returns the filtered, paginated task list.
func (q *queries) ListTasks(ctx context.Context, query TaskQuery) (TaskPage, error) {
	spans, err := q.fetch(ctx, query.Window)
	if err != nil {
		return TaskPage{}, err
	}
	groups := buildGroups(spans)
	tasks := make([]Task, 0, len(groups))
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return TaskPage{}, err
		}
		task := deriveTask(g)
		if !matchesTaskQuery(task, query) {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].StartedAt.After(tasks[j].StartedAt)
	})
	total := len(tasks)
	limit := query.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(max(limit, 1), maxLimit)
	offset := max(query.Offset, 0)
	items := []Task{}
	if offset < len(tasks) {
		items = tasks[offset:min(offset+limit, len(tasks))]
	}
	return TaskPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// matchesTaskQuery reports whether a task passes the query filters.
func matchesTaskQuery(task Task, query TaskQuery) bool {
	if query.Status != "" && task.Status != query.Status {
		return false
	}
	if query.Agent != "" && !taskInvolvesAgent(task, query.Agent) {
		return false
	}
	if query.TaskID != "" && !taskIDMatches(task, query.TaskID) {
		return false
	}
	if query.MinCostUSD != nil || query.MaxCostUSD != nil {
		// An unknown cost cannot satisfy a cost filter.
		if task.CostUSD == nil {
			return false
		}
		cost := *task.CostUSD
		if query.MinCostUSD != nil && cost < *query.MinCostUSD {
			return false
		}
		if query.MaxCostUSD != nil && cost > *query.MaxCostUSD {
			return false
		}
	}
	return true
}

// taskInvolvesAgent reports whether an agent took part in a task, either
// as a callee or as the root caller.
func taskInvolvesAgent(task Task, agent string) bool {
	for _, a := range task.Agents {
		if strings.EqualFold(a, agent) {
			return true
		}
	}
	return strings.EqualFold(task.RootCaller, agent)
}

// taskIDMatches reports whether a task id (or trace id) starts with the
// requested prefix.
func taskIDMatches(task Task, prefix string) bool {
	prefix = strings.ToLower(prefix)
	if strings.HasPrefix(strings.ToLower(task.TaskID), prefix) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(task.TraceID), prefix)
}

// ---------------------------------------------------------------------------
// TaskDetail
// ---------------------------------------------------------------------------

// TaskDetail returns the full picture of one task.
func (q *queries) TaskDetail(ctx context.Context, taskID string) (*TaskDetail, error) {
	spans, err := q.fetch(ctx, Window{})
	if err != nil {
		return nil, err
	}
	groups := buildGroups(spans)
	var (
		target *spanGroup
		task   Task
	)
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate := deriveTask(g)
		if strings.EqualFold(candidate.TaskID, taskID) || strings.EqualFold(g.traceID, taskID) {
			target = g
			task = candidate
			break
		}
	}
	if target == nil {
		return nil, ErrNotFound
	}
	detail := &TaskDetail{
		Task:      task,
		Spans:     []Span{},
		RootCause: rootCause(target),
		Cost:      taskCostReport(target),
		Events:    []Event{},
	}
	for _, s := range target.spans {
		detail.Spans = append(detail.Spans, toSpan(s))
	}
	for _, s := range target.spans {
		errType := s.Attributes[telemetry.AttrErrorType]
		if errType == "" && s.StatusMessage == "" {
			continue
		}
		name := "status"
		if errType != "" {
			name = "exception"
		}
		detail.Events = append(detail.Events, Event{
			SpanID:     s.SpanID,
			Time:       s.StartedAt,
			Name:       name,
			Attributes: copyAttrs(s.Attributes),
		})
	}
	return detail, nil
}

// toSpan converts a span record into the dashboard representation.
func toSpan(rec telemetry.SpanRecord) Span {
	return Span{
		TraceID:       rec.TraceID,
		SpanID:        rec.SpanID,
		ParentSpanID:  rec.ParentSpanID,
		Name:          rec.Name,
		Kind:          rec.Kind,
		ServiceName:   rec.ServiceName,
		Agent:         rec.Attributes[telemetry.AttrCalleeAgent],
		Caller:        rec.Attributes[telemetry.AttrCallerAgent],
		ErrorType:     rec.Attributes[telemetry.AttrErrorType],
		StartedAt:     rec.StartedAt,
		DurationMS:    durationMS(rec.Duration()),
		StatusCode:    rec.StatusCode,
		StatusMessage: rec.StatusMessage,
		Attributes:    copyAttrs(rec.Attributes),
	}
}

// copyAttrs copies span attributes. The result is never nil, so the
// dashboard always receives an object.
func copyAttrs(attrs map[string]string) map[string]string {
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

// rootCause returns the heuristic root cause of a task, or nil when the
// task has no failing span.
//
// The hint is the earliest failing span of the tree. The affected spans
// are the ones that ran after it plus its own descendants.
func rootCause(g *spanGroup) *RootCause {
	fail := earliestFailure(g.spans)
	if fail == nil {
		return nil
	}
	descendants := descendantIDs(g.spans, fail.SpanID)
	affected := make([]string, 0, len(g.spans))
	for _, s := range g.spans {
		if s.SpanID == fail.SpanID {
			continue
		}
		if !s.StartedAt.Before(fail.StartedAt) || descendants[s.SpanID] {
			affected = append(affected, s.SpanID)
		}
	}
	agent := fail.Attributes[telemetry.AttrCalleeAgent]
	errType := fail.Attributes[telemetry.AttrErrorType]
	return &RootCause{
		SpanID:          fail.SpanID,
		SpanName:        fail.Name,
		Agent:           agent,
		ErrorType:       errType,
		Message:         rootCauseMessage(fail, agent, errType, len(affected)),
		AffectedSpanIDs: affected,
		Confidence:      rootCauseConfidence,
	}
}

// descendantIDs returns the ids of the spans whose parent chain reaches
// the given span.
func descendantIDs(spans []telemetry.SpanRecord, spanID string) map[string]bool {
	children := make(map[string][]string)
	for _, s := range spans {
		if s.ParentSpanID != "" {
			children[s.ParentSpanID] = append(children[s.ParentSpanID], s.SpanID)
		}
	}
	descendants := map[string]bool{}
	queue := []string{spanID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, child := range children[id] {
			if descendants[child] {
				continue
			}
			descendants[child] = true
			queue = append(queue, child)
		}
	}
	return descendants
}

// rootCauseMessage renders the plain-language summary of a root-cause
// hint.
func rootCauseMessage(fail *telemetry.SpanRecord, agent, errType string, affected int) string {
	label := strings.TrimSpace(fail.Name)
	switch {
	case label == "" && agent != "":
		label = telemetry.OperationInvokeAgent + " " + agent
	case label == "":
		label = "the span"
	case agent != "" && !strings.Contains(label, agent):
		label = label + " (" + agent + ")"
	}
	if errType == "" {
		errType = unknownErrorType
	}
	return fmt.Sprintf("%s failed after %.1fms (error.type=%s); %d later spans are affected.",
		label, durationMS(fail.Duration()), errType, affected)
}

// taskCostReport builds the cost roll-up of one task.
func taskCostReport(g *spanGroup) CostReport {
	report := CostReport{
		GroupBy: groupByTask,
		Spans:   len(g.spans),
		Buckets: []CostBucket{},
	}
	total := &costAccumulator{}
	byAgent := map[string]*costAccumulator{}
	agents := make([]string, 0, len(g.spans))
	for _, s := range g.spans {
		total.add(s)
		agent := s.Attributes[telemetry.AttrCalleeAgent]
		if agent == "" {
			continue
		}
		acc, ok := byAgent[agent]
		if !ok {
			acc = &costAccumulator{}
			byAgent[agent] = acc
			agents = append(agents, agent)
		}
		acc.add(s)
	}
	report.TotalUSD = total.costUSD()
	report.InputTokens = total.inputTokens()
	report.OutputTokens = total.outputTokens()
	report.UnknownSpans = total.unknown
	for _, agent := range agents {
		acc := byAgent[agent]
		report.Buckets = append(report.Buckets, CostBucket{
			Key:          agent,
			CostUSD:      acc.costUSD(),
			Spans:        acc.spans,
			UnknownSpans: acc.unknown,
			InputTokens:  acc.inputTokens(),
			OutputTokens: acc.outputTokens(),
		})
	}
	sort.SliceStable(report.Buckets, func(i, j int) bool {
		return report.Buckets[i].Key < report.Buckets[j].Key
	})
	return report
}

// ---------------------------------------------------------------------------
// Graph
// ---------------------------------------------------------------------------

// Graph returns the agent-to-agent call graph of a window.
func (q *queries) Graph(ctx context.Context, w Window) (*Graph, error) {
	spans, err := q.fetch(ctx, w)
	if err != nil {
		return nil, err
	}
	type nodeAgg struct {
		calls     int
		errors    int
		durations []float64
		cost      costAccumulator
		lastSeen  *time.Time
	}
	type edgeAgg struct {
		calls     int
		errors    int
		durations []float64
		cost      costAccumulator
	}
	nodes := map[string]*nodeAgg{}
	nodeNames := make([]string, 0, len(spans))
	edges := map[[2]string]*edgeAgg{}
	edgeKeys := make([][2]string, 0, len(spans))

	for _, s := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		callee := s.Attributes[telemetry.AttrCalleeAgent]
		caller := s.Attributes[telemetry.AttrCallerAgent]
		if callee != "" {
			node, ok := nodes[callee]
			if !ok {
				node = &nodeAgg{}
				nodes[callee] = node
				nodeNames = append(nodeNames, callee)
			}
			node.calls++
			if isFailing(s) {
				node.errors++
			}
			node.durations = append(node.durations, durationMS(s.Duration()))
			node.cost.add(s)
			seen := s.StartedAt
			if node.lastSeen == nil || seen.After(*node.lastSeen) {
				last := seen
				node.lastSeen = &last
			}
		}
		// Only real agent-to-agent edges are drawn: an unattributed
		// caller ("unknown") is not an agent.
		if caller == "" || callee == "" || strings.EqualFold(caller, telemetry.CallerUnknown) {
			continue
		}
		key := [2]string{caller, callee}
		edge, ok := edges[key]
		if !ok {
			edge = &edgeAgg{}
			edges[key] = edge
			edgeKeys = append(edgeKeys, key)
		}
		edge.calls++
		if isFailing(s) {
			edge.errors++
		}
		edge.durations = append(edge.durations, durationMS(s.Duration()))
		edge.cost.add(s)
	}

	graph := &Graph{Window: w, Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	for _, name := range nodeNames {
		node := nodes[name]
		graph.Nodes = append(graph.Nodes, GraphNode{
			Name:      name,
			Calls:     node.calls,
			Errors:    node.errors,
			ErrorRate: errorRate(node.errors, node.calls),
			P50MS:     percentile(node.durations, 0.5),
			P95MS:     percentile(node.durations, 0.95),
			CostUSD:   node.cost.costUSD(),
			LastSeen:  node.lastSeen,
		})
	}
	sort.SliceStable(graph.Nodes, func(i, j int) bool {
		return graph.Nodes[i].Name < graph.Nodes[j].Name
	})
	for _, key := range edgeKeys {
		edge := edges[key]
		graph.Edges = append(graph.Edges, GraphEdge{
			From:      key[0],
			To:        key[1],
			Calls:     edge.calls,
			Errors:    edge.errors,
			ErrorRate: errorRate(edge.errors, edge.calls),
			P50MS:     percentile(edge.durations, 0.5),
			P95MS:     percentile(edge.durations, 0.95),
			CostUSD:   edge.cost.costUSD(),
		})
	}
	sort.SliceStable(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].From != graph.Edges[j].From {
			return graph.Edges[i].From < graph.Edges[j].From
		}
		return graph.Edges[i].To < graph.Edges[j].To
	})
	return graph, nil
}

// ---------------------------------------------------------------------------
// Costs
// ---------------------------------------------------------------------------

// Costs returns a cost and token roll-up for a window.
func (q *queries) Costs(ctx context.Context, query CostQuery) (*CostReport, error) {
	spans, err := q.fetch(ctx, query.Window)
	if err != nil {
		return nil, err
	}
	groupBy := query.GroupBy
	switch groupBy {
	case groupByAgent, groupByModel, groupByTask, groupByWindow:
	default:
		groupBy = groupByAgent
	}

	// The task grouping needs the task id of every trace tree.
	taskIDs := map[string]string{}
	if groupBy == groupByTask {
		for _, g := range buildGroups(spans) {
			taskIDs[g.traceID] = deriveTask(g).TaskID
		}
	}

	report := &CostReport{GroupBy: groupBy, Spans: len(spans), Buckets: []CostBucket{}}
	total := &costAccumulator{}
	buckets := map[string]*costAccumulator{}
	keys := make([]string, 0, len(spans))

	for _, s := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		total.add(s)
		key := costBucketKey(groupBy, s, taskIDs)
		if key == "" {
			continue
		}
		acc, ok := buckets[key]
		if !ok {
			acc = &costAccumulator{}
			buckets[key] = acc
			keys = append(keys, key)
		}
		acc.add(s)
	}
	report.TotalUSD = total.costUSD()
	report.InputTokens = total.inputTokens()
	report.OutputTokens = total.outputTokens()
	report.UnknownSpans = total.unknown
	for _, key := range keys {
		acc := buckets[key]
		report.Buckets = append(report.Buckets, CostBucket{
			Key:          key,
			CostUSD:      acc.costUSD(),
			Spans:        acc.spans,
			UnknownSpans: acc.unknown,
			InputTokens:  acc.inputTokens(),
			OutputTokens: acc.outputTokens(),
		})
	}
	sort.SliceStable(report.Buckets, func(i, j int) bool {
		return report.Buckets[i].Key < report.Buckets[j].Key
	})
	return report, nil
}

// costBucketKey returns the grouping key of a span.
func costBucketKey(groupBy string, s telemetry.SpanRecord, taskIDs map[string]string) string {
	switch groupBy {
	case groupByModel:
		model := s.Attributes[attrRequestModel]
		if model == "" {
			return unknownBucketKey
		}
		return model
	case groupByTask:
		if id, ok := taskIDs[s.TraceID]; ok {
			return id
		}
		return s.TraceID
	case groupByWindow:
		return s.StartedAt.Truncate(time.Hour).Format(time.RFC3339)
	default:
		return s.Attributes[telemetry.AttrCalleeAgent]
	}
}

// ---------------------------------------------------------------------------
// AgentStats
// ---------------------------------------------------------------------------

// AgentStats returns the per-agent statistics of a window.
func (q *queries) AgentStats(ctx context.Context, w Window) ([]AgentStats, error) {
	spans, err := q.fetch(ctx, w)
	if err != nil {
		return nil, err
	}
	type agentAgg struct {
		calls     int
		errors    int
		durations []float64
		cost      costAccumulator
		lastSeen  *time.Time
	}
	agents := map[string]*agentAgg{}
	names := make([]string, 0, len(spans))
	for _, s := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		agent := s.Attributes[telemetry.AttrCalleeAgent]
		if agent == "" {
			continue
		}
		agg, ok := agents[agent]
		if !ok {
			agg = &agentAgg{}
			agents[agent] = agg
			names = append(names, agent)
		}
		agg.calls++
		if isFailing(s) {
			agg.errors++
		}
		agg.durations = append(agg.durations, durationMS(s.Duration()))
		agg.cost.add(s)
		seen := s.StartedAt
		if agg.lastSeen == nil || seen.After(*agg.lastSeen) {
			last := seen
			agg.lastSeen = &last
		}
	}
	stats := make([]AgentStats, 0, len(names))
	for _, name := range names {
		agg := agents[name]
		stats = append(stats, AgentStats{
			Name:         name,
			Spans:        agg.calls,
			Errors:       agg.errors,
			ErrorRate:    errorRate(agg.errors, agg.calls),
			P50MS:        percentile(agg.durations, 0.5),
			P95MS:        percentile(agg.durations, 0.95),
			CostUSD:      agg.cost.costUSD(),
			InputTokens:  agg.cost.inputTokens(),
			OutputTokens: agg.cost.outputTokens(),
			LastSeen:     agg.lastSeen,
		})
	}
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Name < stats[j].Name
	})
	return stats, nil
}

// ---------------------------------------------------------------------------
// Overview
// ---------------------------------------------------------------------------

// Overview returns the dashboard summary of a window.
func (q *queries) Overview(ctx context.Context, w Window) (*Overview, error) {
	spans, err := q.fetch(ctx, w)
	if err != nil {
		return nil, err
	}
	groups := buildGroups(spans)
	overview := &Overview{
		Window:          w,
		TasksOverTime:   []TimeBucket{},
		CostOverTime:    []TimeBucket{},
		TopAgentsByCost: []CostBucket{},
		TopFailing:      []FailingAgent{},
		RecentFailed:    []Task{},
	}

	durations := make([]float64, 0, len(groups))
	var costSum float64
	costKnown := false
	failed := make([]Task, 0, len(groups))
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		task := deriveTask(g)
		switch task.Status {
		case statusSucceeded:
			overview.TasksSucceeded++
		case statusFailed:
			overview.TasksFailed++
			failed = append(failed, task)
		}
		durations = append(durations, task.DurationMS)
		if task.CostUSD != nil {
			costKnown = true
			costSum += *task.CostUSD
		}
	}
	overview.TasksTotal = len(groups)
	overview.ErrorRate = errorRate(overview.TasksFailed, overview.TasksTotal)
	overview.P50TaskMS = percentile(durations, 0.5)
	overview.P95TaskMS = percentile(durations, 0.95)
	if costKnown {
		total := costSum
		overview.TotalCostUSD = &total
	}

	agentNames := map[string]struct{}{}
	for _, s := range spans {
		if agent := s.Attributes[telemetry.AttrCalleeAgent]; agent != "" {
			agentNames[agent] = struct{}{}
		}
	}
	overview.ActiveAgents = len(agentNames)
	overview.SpansTotal = len(spans)

	// Time series.
	plan := planBuckets(w, time.Now())
	starts := plan.starts()
	index := make(map[time.Time]int, len(starts))
	taskBuckets := make([]TimeBucket, len(starts))
	costBuckets := make([]TimeBucket, len(starts))
	for i, start := range starts {
		index[start] = i
		taskBuckets[i] = TimeBucket{Bucket: start}
		costBuckets[i] = TimeBucket{Bucket: start}
	}
	for _, g := range groups {
		task := deriveTask(g)
		i, ok := index[task.StartedAt.Truncate(plan.size)]
		if !ok {
			continue
		}
		switch task.Status {
		case statusSucceeded:
			taskBuckets[i].Succeeded++
		case statusFailed:
			taskBuckets[i].Failed++
		default:
			taskBuckets[i].Unknown++
		}
		if task.CostUSD != nil {
			taskBuckets[i].CostUSD = addCost(taskBuckets[i].CostUSD, *task.CostUSD)
		}
	}
	for _, s := range spans {
		i, ok := index[s.StartedAt.Truncate(plan.size)]
		if !ok {
			continue
		}
		costBuckets[i].Spans++
		if value, ok := parseFloatAttr(s, attrCostUSD); ok {
			costBuckets[i].CostUSD = addCost(costBuckets[i].CostUSD, value)
		}
	}
	overview.TasksOverTime = taskBuckets
	overview.CostOverTime = costBuckets

	// Top agents by cost.
	overview.TopAgentsByCost = topAgentsByCost(spans)

	// Top failing agents.
	overview.TopFailing = topFailingAgents(spans)

	// Recent failed tasks, newest first.
	sort.SliceStable(failed, func(i, j int) bool {
		return failed[i].StartedAt.After(failed[j].StartedAt)
	})
	if len(failed) > 10 {
		failed = failed[:10]
	}
	overview.RecentFailed = failed
	return overview, nil
}

// topAgentsByCost ranks the agents of a window by their known cost.
func topAgentsByCost(spans []telemetry.SpanRecord) []CostBucket {
	agents := map[string]*costAccumulator{}
	names := make([]string, 0, len(spans))
	for _, s := range spans {
		agent := s.Attributes[telemetry.AttrCalleeAgent]
		if agent == "" {
			continue
		}
		acc, ok := agents[agent]
		if !ok {
			acc = &costAccumulator{}
			agents[agent] = acc
			names = append(names, agent)
		}
		acc.add(s)
	}
	buckets := make([]CostBucket, 0, len(names))
	for _, name := range names {
		acc := agents[name]
		buckets = append(buckets, CostBucket{
			Key:          name,
			CostUSD:      acc.costUSD(),
			Spans:        acc.spans,
			UnknownSpans: acc.unknown,
			InputTokens:  acc.inputTokens(),
			OutputTokens: acc.outputTokens(),
		})
	}
	// Known cost first (highest first); unknown cost is ranked last and
	// only makes the cut when there is room left.
	sort.SliceStable(buckets, func(i, j int) bool {
		ci, cj := buckets[i].CostUSD, buckets[j].CostUSD
		switch {
		case ci != nil && cj != nil:
			if *ci != *cj {
				return *ci > *cj
			}
			return buckets[i].Key < buckets[j].Key
		case ci != nil:
			return true
		case cj != nil:
			return false
		default:
			return buckets[i].Key < buckets[j].Key
		}
	})
	if len(buckets) > 5 {
		buckets = buckets[:5]
	}
	return buckets
}

// topFailingAgents ranks the agents of a window by their error count.
func topFailingAgents(spans []telemetry.SpanRecord) []FailingAgent {
	type failing struct {
		errors int
		calls  int
	}
	agents := map[string]*failing{}
	names := make([]string, 0, len(spans))
	for _, s := range spans {
		agent := s.Attributes[telemetry.AttrCalleeAgent]
		if agent == "" {
			continue
		}
		agg, ok := agents[agent]
		if !ok {
			agg = &failing{}
			agents[agent] = agg
			names = append(names, agent)
		}
		agg.calls++
		if isFailing(s) {
			agg.errors++
		}
	}
	list := make([]FailingAgent, 0, len(names))
	for _, name := range names {
		agg := agents[name]
		if agg.errors == 0 {
			continue
		}
		list = append(list, FailingAgent{
			Name:      name,
			Errors:    agg.errors,
			ErrorRate: errorRate(agg.errors, agg.calls),
		})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Errors != list[j].Errors {
			return list[i].Errors > list[j].Errors
		}
		return list[i].Name < list[j].Name
	})
	if len(list) > 5 {
		list = list[:5]
	}
	return list
}

// ---------------------------------------------------------------------------
// time buckets
// ---------------------------------------------------------------------------

// bucketPlan describes the time series of a window.
type bucketPlan struct {
	from time.Time
	to   time.Time
	size time.Duration
}

// planBuckets picks the bucket size of a window.
//
// The size follows the window length: minutes for the last hour, five
// minutes for half a day, hours for two days, days beyond that. An
// unbounded window is treated as the last 24 hours.
func planBuckets(w Window, now time.Time) bucketPlan {
	to := w.To
	if to.IsZero() {
		to = now
	}
	from := w.From
	if from.IsZero() {
		from = to.Add(-defaultWindow)
	}
	return bucketPlan{from: from, to: to, size: bucketSize(to.Sub(from))}
}

// bucketSize returns the time series bucket size of a window length.
func bucketSize(length time.Duration) time.Duration {
	switch {
	case length <= time.Hour:
		return time.Minute
	case length <= 6*time.Hour:
		return 5 * time.Minute
	case length <= 48*time.Hour:
		return time.Hour
	default:
		return 24 * time.Hour
	}
}

// starts returns the bucket starts inside the window.
func (p bucketPlan) starts() []time.Time {
	starts := make([]time.Time, 0, 16)
	start := p.from.Truncate(p.size)
	if start.Before(p.from) {
		start = start.Add(p.size)
	}
	for start.Before(p.to) {
		starts = append(starts, start)
		start = start.Add(p.size)
	}
	return starts
}

// ---------------------------------------------------------------------------
// cost accumulation
// ---------------------------------------------------------------------------

// costAccumulator sums cost and token usage over a group of spans.
//
// A missing value stays missing: the accumulator knows whether anything
// was reported at all, so an unknown cost never becomes a zero.
type costAccumulator struct {
	spans   int
	unknown int
	known   int
	hasCost bool
	cost    float64
	hasIn   bool
	in      int64
	hasOut  bool
	out     int64
}

// add folds one span into the accumulator.
func (c *costAccumulator) add(s telemetry.SpanRecord) {
	c.spans++
	if tokensKnown(s) {
		c.known++
	} else {
		c.unknown++
	}
	if value, ok := parseFloatAttr(s, attrCostUSD); ok {
		c.hasCost = true
		c.cost += value
	}
	if value, ok := parseIntAttr(s, attrUsageInput); ok {
		c.hasIn = true
		c.in += value
	}
	if value, ok := parseIntAttr(s, attrUsageOutput); ok {
		c.hasOut = true
		c.out += value
	}
}

// costUSD returns the summed cost, or nil when nothing was reported.
func (c *costAccumulator) costUSD() *float64 {
	if !c.hasCost {
		return nil
	}
	value := c.cost
	return &value
}

// inputTokens returns the summed input tokens, or nil when unknown.
func (c *costAccumulator) inputTokens() *int64 {
	if !c.hasIn {
		return nil
	}
	value := c.in
	return &value
}

// outputTokens returns the summed output tokens, or nil when unknown.
func (c *costAccumulator) outputTokens() *int64 {
	if !c.hasOut {
		return nil
	}
	value := c.out
	return &value
}

// addCost adds a cost to a bucket that may not have one yet.
func addCost(current *float64, value float64) *float64 {
	if current == nil {
		sum := value
		return &sum
	}
	sum := *current + value
	return &sum
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// fetch reads the spans of a window from the store.
//
// The store applies the window; the query layer applies it again for the
// bounds that are set, so a store with a laxer window still yields the
// same answer. A zero From is unbounded and a zero To is unbounded here
// as well: the store decides what "now" means.
func (q *queries) fetch(ctx context.Context, w Window) ([]telemetry.SpanRecord, error) {
	if q.store == nil {
		return nil, errNoStore
	}
	spans, err := q.store.Spans(ctx, SpanQuery{Window: w})
	if err != nil {
		return nil, err
	}
	out := make([]telemetry.SpanRecord, 0, len(spans))
	for _, s := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !w.From.IsZero() && s.StartedAt.Before(w.From) {
			continue
		}
		if !w.To.IsZero() && !s.StartedAt.Before(w.To) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// tokensKnown reports whether a span states that its token usage is
// known.
func tokensKnown(s telemetry.SpanRecord) bool {
	return strings.EqualFold(strings.TrimSpace(s.Attributes[telemetry.AttrTokensKnown]), "true")
}

// parseFloatAttr reads a numeric attribute. Malformed values are ignored:
// a broken span must not break an aggregation.
func parseFloatAttr(s telemetry.SpanRecord, key string) (float64, bool) {
	raw, ok := s.Attributes[key]
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// parseIntAttr reads an integer attribute. Malformed values are ignored.
func parseIntAttr(s telemetry.SpanRecord, key string) (int64, bool) {
	raw, ok := s.Attributes[key]
	if !ok {
		return 0, false
	}
	trimmed := strings.TrimSpace(raw)
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err == nil {
		return value, true
	}
	// Tolerate a float that carries an integral value ("12.0").
	float, ferr := strconv.ParseFloat(trimmed, 64)
	if ferr != nil {
		return 0, false
	}
	return int64(float), true
}

// durationMS converts a duration to milliseconds.
func durationMS(d time.Duration) float64 {
	return float64(d.Nanoseconds()) / float64(time.Millisecond)
}

// errorRate returns errors over calls, zero when there were no calls.
func errorRate(errors, calls int) float64 {
	if calls == 0 {
		return 0
	}
	return float64(errors) / float64(calls)
}

// percentile returns the nearest-rank percentile of a sample: the value at
// index ceil(q*n)-1 of the sorted sample.
func percentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	index := int(math.Ceil(q*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
