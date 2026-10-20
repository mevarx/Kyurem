package mgmtapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/storage"
)

// ---------------------------------------------------------------- test rig

const testToken = "s3cret-token"

// fixedNow is the clock every test sees.
var fixedNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// fakeQueries is an in-memory storage.Queries. The management API tests
// never touch the real storage implementation: the query layer is written
// in parallel and only its contract matters here.
type fakeQueries struct {
	listTasks  func(ctx context.Context, q storage.TaskQuery) (storage.TaskPage, error)
	taskDetail func(ctx context.Context, taskID string) (*storage.TaskDetail, error)
	graph      func(ctx context.Context, w storage.Window) (*storage.Graph, error)
	costs      func(ctx context.Context, q storage.CostQuery) (*storage.CostReport, error)
	agentStats func(ctx context.Context, w storage.Window) ([]storage.AgentStats, error)
	overview   func(ctx context.Context, w storage.Window) (*storage.Overview, error)

	mu          sync.Mutex
	taskQuery   storage.TaskQuery
	taskID      string
	costQuery   storage.CostQuery
	window      storage.Window
	agentWindow storage.Window
}

func (f *fakeQueries) ListTasks(ctx context.Context, q storage.TaskQuery) (storage.TaskPage, error) {
	f.mu.Lock()
	f.taskQuery = q
	f.mu.Unlock()
	if f.listTasks != nil {
		return f.listTasks(ctx, q)
	}
	return storage.TaskPage{}, nil
}

func (f *fakeQueries) TaskDetail(ctx context.Context, taskID string) (*storage.TaskDetail, error) {
	f.mu.Lock()
	f.taskID = taskID
	f.mu.Unlock()
	if f.taskDetail != nil {
		return f.taskDetail(ctx, taskID)
	}
	return nil, storage.ErrNotFound
}

func (f *fakeQueries) Graph(ctx context.Context, w storage.Window) (*storage.Graph, error) {
	f.mu.Lock()
	f.window = w
	f.mu.Unlock()
	if f.graph != nil {
		return f.graph(ctx, w)
	}
	return &storage.Graph{}, nil
}

func (f *fakeQueries) Costs(ctx context.Context, q storage.CostQuery) (*storage.CostReport, error) {
	f.mu.Lock()
	f.costQuery = q
	f.mu.Unlock()
	if f.costs != nil {
		return f.costs(ctx, q)
	}
	return &storage.CostReport{}, nil
}

func (f *fakeQueries) AgentStats(ctx context.Context, w storage.Window) ([]storage.AgentStats, error) {
	f.mu.Lock()
	f.agentWindow = w
	f.mu.Unlock()
	if f.agentStats != nil {
		return f.agentStats(ctx, w)
	}
	return nil, nil
}

func (f *fakeQueries) Overview(ctx context.Context, w storage.Window) (*storage.Overview, error) {
	f.mu.Lock()
	f.window = w
	f.mu.Unlock()
	if f.overview != nil {
		return f.overview(ctx, w)
	}
	return &storage.Overview{}, nil
}

// recordedTaskQuery returns the query the fake received.
func (f *fakeQueries) recordedTaskQuery() storage.TaskQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.taskQuery
}

// recordedCostQuery returns the cost query the fake received.
func (f *fakeQueries) recordedCostQuery() storage.CostQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.costQuery
}

// newTestHandler builds the management API with a fixed clock and a quiet
// logger.
func newTestHandler(t *testing.T, q storage.Queries, mutate func(*Deps)) http.Handler {
	t.Helper()
	deps := Deps{
		Queries:         q,
		AuthToken:       testToken,
		StorageDriver:   "memory",
		StorageDatabase: "otel",
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:             func() time.Time { return fixedNow },
		Uptime:          fixedNow.Add(-90 * time.Second),
	}
	if mutate != nil {
		mutate(&deps)
	}
	return New(deps)
}

// do performs a request against the handler.
func do(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeError decodes the standard error envelope.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body %q)", err, rec.Body.String())
	}
	if env.Error.Code == "" {
		t.Fatalf("error envelope without code: %q", rec.Body.String())
	}
	if env.Error.Message == "" {
		t.Fatalf("error envelope without message: %q", rec.Body.String())
	}
	return env
}

// testRegistry builds a registry with the given agents.
func testRegistry(t *testing.T, agents ...config.AgentReg) *registry.Registry {
	t.Helper()
	cfg := &config.Config{
		Listeners: config.Listeners{A2A: config.A2AListener{RoutePrefix: "/a2a"}},
		Agents:    agents,
	}
	reg, err := registry.New(cfg)
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	return reg
}

// ------------------------------------------------------------------- tests

func TestAuth(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, nil)

	rec := do(t, h, http.MethodGet, "/api/v1/overview", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: got %d, want 401", rec.Code)
	}
	if env := decodeError(t, rec); env.Error.Code != "unauthorized" {
		t.Fatalf("missing token: code %q, want unauthorized", env.Error.Code)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/overview", "wrong-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", rec.Code)
	}
	if env := decodeError(t, rec); env.Error.Code != "unauthorized" {
		t.Fatalf("wrong token: code %q, want unauthorized", env.Error.Code)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/overview", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct token: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestAuthSchemeIsCaseInsensitive(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.Header.Set("Authorization", "bEaReR "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("lowercase scheme: got %d, want 200", rec.Code)
	}
}

func TestAuthOpenAPIWithoutToken(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, func(d *Deps) { d.AuthToken = "" })
	rec := do(t, h, http.MethodGet, "/api/v1/overview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("open API: got %d, want 200", rec.Code)
	}
}

func TestTasksQueryParameters(t *testing.T) {
	fq := &fakeQueries{}
	h := newTestHandler(t, fq, nil)

	rec := do(t, h, http.MethodGet,
		"/api/v1/tasks?limit=10&offset=5&status=failed&agent=planner&task_id=abc&min_cost_usd=1.5&max_cost_usd=9.25",
		testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("tasks: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	q := fq.recordedTaskQuery()
	if q.Limit != 10 {
		t.Errorf("limit: got %d, want 10", q.Limit)
	}
	if q.Offset != 5 {
		t.Errorf("offset: got %d, want 5", q.Offset)
	}
	if q.Status != "failed" {
		t.Errorf("status: got %q, want failed", q.Status)
	}
	if q.Agent != "planner" {
		t.Errorf("agent: got %q, want planner", q.Agent)
	}
	if q.TaskID != "abc" {
		t.Errorf("task_id: got %q, want abc", q.TaskID)
	}
	if q.MinCostUSD == nil || *q.MinCostUSD != 1.5 {
		t.Errorf("min_cost_usd: got %v, want 1.5", q.MinCostUSD)
	}
	if q.MaxCostUSD == nil || *q.MaxCostUSD != 9.25 {
		t.Errorf("max_cost_usd: got %v, want 9.25", q.MaxCostUSD)
	}
	// The default window is the last 24 hours.
	if want := fixedNow.Add(-24 * time.Hour); !q.From.Equal(want) {
		t.Errorf("from: got %s, want %s", q.From, want)
	}
	if !q.To.Equal(fixedNow) {
		t.Errorf("to: got %s, want %s", q.To, fixedNow)
	}
}

func TestTasksDefaultsAndClamping(t *testing.T) {
	fq := &fakeQueries{}
	h := newTestHandler(t, fq, nil)

	if rec := do(t, h, http.MethodGet, "/api/v1/tasks", testToken); rec.Code != http.StatusOK {
		t.Fatalf("tasks: got %d, want 200", rec.Code)
	}
	if q := fq.recordedTaskQuery(); q.Limit != 50 || q.Offset != 0 {
		t.Errorf("defaults: got limit %d offset %d, want 50/0", q.Limit, q.Offset)
	}

	if rec := do(t, h, http.MethodGet, "/api/v1/tasks?limit=100000", testToken); rec.Code != http.StatusOK {
		t.Fatalf("tasks: got %d, want 200", rec.Code)
	}
	if q := fq.recordedTaskQuery(); q.Limit != 500 {
		t.Errorf("clamped limit: got %d, want 500", q.Limit)
	}

	if rec := do(t, h, http.MethodGet, "/api/v1/tasks?limit=-3", testToken); rec.Code != http.StatusOK {
		t.Fatalf("tasks: got %d, want 200", rec.Code)
	}
	if q := fq.recordedTaskQuery(); q.Limit != 1 {
		t.Errorf("clamped limit: got %d, want 1", q.Limit)
	}

	if rec := do(t, h, http.MethodGet, "/api/v1/tasks?offset=-9", testToken); rec.Code != http.StatusOK {
		t.Fatalf("tasks: got %d, want 200", rec.Code)
	}
	if q := fq.recordedTaskQuery(); q.Offset != 0 {
		t.Errorf("clamped offset: got %d, want 0", q.Offset)
	}
}

func TestTasksInvalidParameters(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, nil)

	cases := []struct {
		name string
		path string
	}{
		{"bad status", "/api/v1/tasks?status=weird"},
		{"bad from", "/api/v1/tasks?from=not-a-time"},
		{"bad to", "/api/v1/tasks?to=13x"},
		{"bad min cost", "/api/v1/tasks?min_cost_usd=cheap"},
		{"bad max cost", "/api/v1/tasks?max_cost_usd=expensive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, tc.path, testToken)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400 (body %q)", rec.Code, rec.Body.String())
			}
			if env := decodeError(t, rec); env.Error.Code != "invalid_parameter" {
				t.Fatalf("code %q, want invalid_parameter", env.Error.Code)
			}
		})
	}
}

func TestTaskDetail(t *testing.T) {
	fq := &fakeQueries{
		taskDetail: func(ctx context.Context, taskID string) (*storage.TaskDetail, error) {
			return &storage.TaskDetail{
				Task:  storage.Task{TaskID: taskID, Status: "succeeded"},
				Spans: []storage.Span{{TraceID: "t", SpanID: "s", Name: "invoke_agent"}},
			}, nil
		},
	}
	h := newTestHandler(t, fq, nil)

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/task-1", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var detail storage.TaskDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Task.TaskID != "task-1" {
		t.Errorf("task id: got %q, want task-1", detail.Task.TaskID)
	}
	if len(detail.Spans) != 1 {
		t.Errorf("spans: got %d, want 1", len(detail.Spans))
	}
	if fq.taskID != "task-1" {
		t.Errorf("forwarded id: got %q, want task-1", fq.taskID)
	}
}

func TestTaskDetailNotFound(t *testing.T) {
	fq := &fakeQueries{
		taskDetail: func(ctx context.Context, taskID string) (*storage.TaskDetail, error) {
			return nil, storage.ErrNotFound
		},
	}
	h := newTestHandler(t, fq, nil)

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/missing", testToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not found: got %d, want 404 (body %q)", rec.Code, rec.Body.String())
	}
	env := decodeError(t, rec)
	if env.Error.Code != "not_found" {
		t.Errorf("code %q, want not_found", env.Error.Code)
	}
	if env.Error.Message != "task not found" {
		t.Errorf("message %q, want %q", env.Error.Message, "task not found")
	}
}

func TestTaskDetailInternalError(t *testing.T) {
	fq := &fakeQueries{
		taskDetail: func(ctx context.Context, taskID string) (*storage.TaskDetail, error) {
			return nil, errors.New("clickhouse: password=hunter2 connection refused")
		},
	}
	h := newTestHandler(t, fq, nil)

	rec := do(t, h, http.MethodGet, "/api/v1/tasks/task-1", testToken)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("internal error: got %d, want 500 (body %q)", rec.Code, rec.Body.String())
	}
	env := decodeError(t, rec)
	if env.Error.Code != "internal_error" {
		t.Errorf("code %q, want internal_error", env.Error.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "clickhouse") {
		t.Fatalf("internal details leaked: %q", rec.Body.String())
	}
}

func TestAgents(t *testing.T) {
	reg := testRegistry(t,
		config.AgentReg{
			Name:      "worker",
			Aliases:   []string{"w"},
			Tags:      []string{"io"},
			Upstreams: []config.Upstream{{URL: "http://worker:9000"}},
		},
		config.AgentReg{
			Name:      "planner",
			Aliases:   []string{"plan"},
			Tags:      []string{"core"},
			Upstreams: []config.Upstream{{URL: "http://planner:9000"}, {URL: "http://planner-backup:9000"}},
		},
	)
	fq := &fakeQueries{
		agentStats: func(ctx context.Context, w storage.Window) ([]storage.AgentStats, error) {
			cost := 0.25
			last := fixedNow.Add(-time.Minute)
			return []storage.AgentStats{
				{Name: "worker", Spans: 3, Errors: 1, ErrorRate: 0.33, P50MS: 10, P95MS: 20, CostUSD: &cost, LastSeen: &last},
				{Name: "planner", Spans: 7, Errors: 0, ErrorRate: 0, P50MS: 5, P95MS: 9},
			}, nil
		},
	}
	h := newTestHandler(t, fq, func(d *Deps) { d.Registry = reg })

	rec := do(t, h, http.MethodGet, "/api/v1/agents", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("agents: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var resp agentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items: got %d, want 2", len(resp.Items))
	}
	// Sorted by name.
	if resp.Items[0].Name != "planner" || resp.Items[1].Name != "worker" {
		t.Fatalf("items not sorted by name: %q, %q", resp.Items[0].Name, resp.Items[1].Name)
	}
	planner := resp.Items[0]
	if planner.Path != "/a2a/planner" {
		t.Errorf("path: got %q, want /a2a/planner", planner.Path)
	}
	if len(planner.Aliases) != 1 || planner.Aliases[0] != "plan" {
		t.Errorf("aliases: got %v, want [plan]", planner.Aliases)
	}
	if len(planner.Upstreams) != 2 || planner.Upstreams[0] != "http://planner:9000" {
		t.Errorf("upstreams: got %v", planner.Upstreams)
	}
	if planner.CardStatus != "unknown" {
		t.Errorf("card_status: got %q, want unknown", planner.CardStatus)
	}
	if planner.CardFetchedAt != nil {
		t.Errorf("card_fetched_at: got %v, want null", planner.CardFetchedAt)
	}
	if planner.Verification != "unchecked" {
		t.Errorf("verification: got %q, want unchecked", planner.Verification)
	}
	if planner.BreakerState != "unknown" {
		t.Errorf("breaker_state: got %q, want unknown", planner.BreakerState)
	}
	if planner.Spans != 7 || planner.Errors != 0 || planner.ErrorRate != 0 {
		t.Errorf("planner stats: got %d/%d/%v", planner.Spans, planner.Errors, planner.ErrorRate)
	}
	worker := resp.Items[1]
	if worker.Spans != 3 || worker.Errors != 1 {
		t.Errorf("worker stats: got %d/%d, want 3/1", worker.Spans, worker.Errors)
	}
	if worker.CostUSD == nil || *worker.CostUSD != 0.25 {
		t.Errorf("worker cost: got %v, want 0.25", worker.CostUSD)
	}
	if worker.LastSeen == nil {
		t.Error("worker last_seen: got null, want a timestamp")
	}
}

func TestAgentsEmptyRegistry(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, nil)

	rec := do(t, h, http.MethodGet, "/api/v1/agents", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("agents: got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("items must be an empty array, got %q", rec.Body.String())
	}
}

func TestAgentsCardStatus(t *testing.T) {
	card := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"planner","description":"d","version":"1.0.0",`+
			`"capabilities":{},"defaultInputModes":["text/plain"],`+
			`"defaultOutputModes":["text/plain"],"skills":[]}`)
	}))
	defer card.Close()

	reg := testRegistry(t, config.AgentReg{
		Name:      "planner",
		Upstreams: []config.Upstream{{URL: card.URL}},
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache := registry.NewCardCache(reg, card.Client(), time.Minute, time.Minute, 1<<20, logger)
	if _, err := cache.Get(context.Background(), reg.Entries()[0]); err != nil {
		t.Fatalf("prime card cache: %v", err)
	}

	h := newTestHandler(t, &fakeQueries{}, func(d *Deps) {
		d.Registry = reg
		d.Cards = cache
	})
	rec := do(t, h, http.MethodGet, "/api/v1/agents", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("agents: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var resp agentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: got %d, want 1", len(resp.Items))
	}
	if resp.Items[0].CardStatus != "ok" {
		t.Errorf("card_status: got %q, want ok", resp.Items[0].CardStatus)
	}
	if resp.Items[0].CardFetchedAt == nil {
		t.Error("card_fetched_at: got null, want a timestamp")
	}
}

func TestSettings(t *testing.T) {
	reg := testRegistry(t, config.AgentReg{Name: "planner", Upstreams: []config.Upstream{{URL: "http://planner:9000"}}})
	mtr := metrics.New()
	mtr.SpanEnded()
	mtr.SpanEnded()
	mtr.SpanExported(1)
	mtr.SpanDropped("queue_full", 3)
	mtr.ExportFailed(1)
	mtr.ExportRetried()
	mtr.SetQueueDepth(7, 2048)

	h := newTestHandler(t, &fakeQueries{}, func(d *Deps) {
		d.Registry = reg
		d.Metrics = mtr
	})
	rec := do(t, h, http.MethodGet, "/api/v1/settings", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	for _, key := range []string{"version", "spec_version", "a2a_go_sdk_version", "service_name",
		"uptime_seconds", "agents", "telemetry", "storage"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("settings: missing key %q", key)
		}
	}
	var resp settingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if resp.Version == "" || resp.SpecVersion == "" {
		t.Errorf("versions: got %q/%q, want non-empty", resp.Version, resp.SpecVersion)
	}
	if resp.ServiceName == "" {
		t.Error("service_name: want non-empty")
	}
	if resp.Agents != 1 {
		t.Errorf("agents: got %d, want 1", resp.Agents)
	}
	if resp.UptimeSeconds != 90 {
		t.Errorf("uptime_seconds: got %v, want 90", resp.UptimeSeconds)
	}
	if resp.Storage.Driver != "memory" || resp.Storage.Database != "otel" {
		t.Errorf("storage: got %+v", resp.Storage)
	}
	if resp.Telemetry.SpansEnded != 2 {
		t.Errorf("spans_ended: got %d, want 2", resp.Telemetry.SpansEnded)
	}
	if resp.Telemetry.SpansExported != 1 {
		t.Errorf("spans_exported: got %d, want 1", resp.Telemetry.SpansExported)
	}
	if resp.Telemetry.SpansDropped["queue_full"] != 3 {
		t.Errorf("spans_dropped: got %v, want queue_full=3", resp.Telemetry.SpansDropped)
	}
	if resp.Telemetry.ExportFailures != 1 || resp.Telemetry.ExportRetries != 1 {
		t.Errorf("export failures/retries: got %d/%d, want 1/1",
			resp.Telemetry.ExportFailures, resp.Telemetry.ExportRetries)
	}
	if resp.Telemetry.QueueDepth != 7 || resp.Telemetry.QueueCapacity != 2048 {
		t.Errorf("queue depth/capacity: got %d/%d, want 7/2048",
			resp.Telemetry.QueueDepth, resp.Telemetry.QueueCapacity)
	}
}

func TestSettingsWithoutMetricsAndTelemetry(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, func(d *Deps) {
		d.Metrics = nil
		d.Telemetry = nil
	})
	rec := do(t, h, http.MethodGet, "/api/v1/settings", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	tel, ok := raw["telemetry"].(map[string]any)
	if !ok {
		t.Fatalf("telemetry: got %v, want an object", raw["telemetry"])
	}
	for _, key := range []string{"exporting", "spans_ended", "spans_exported", "spans_dropped"} {
		if _, ok := tel[key]; !ok {
			t.Errorf("telemetry: missing key %q", key)
		}
	}
	dropped, ok := tel["spans_dropped"].(map[string]any)
	if !ok {
		t.Fatalf("spans_dropped: got %v, want an object", tel["spans_dropped"])
	}
	if len(dropped) != 0 {
		t.Errorf("spans_dropped: got %v, want an empty map", dropped)
	}
	st, ok := raw["storage"].(map[string]any)
	if !ok {
		t.Fatalf("storage: got %v, want an object", raw["storage"])
	}
	if st["driver"] != "memory" {
		t.Errorf("storage.driver: got %v, want memory", st["driver"])
	}
}

func TestHealthAndReady(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, func(d *Deps) {
		d.Registry = testRegistry(t, config.AgentReg{
			Name:      "planner",
			Upstreams: []config.Upstream{{URL: "http://planner:9000"}},
		})
	})

	rec := do(t, h, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: got %d, want 200", rec.Code)
	}
	var health healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("healthz status: got %q, want ok", health.Status)
	}
	if health.Agents != nil {
		t.Errorf("healthz agents: got %v, want absent", *health.Agents)
	}

	rec = do(t, h, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz: got %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode readyz: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("readyz status: got %q, want ok", health.Status)
	}
	if health.Agents == nil || *health.Agents != 1 {
		t.Errorf("readyz agents: got %v, want 1", health.Agents)
	}
}

func TestUnknownPath(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, nil)

	rec := do(t, h, http.MethodGet, "/api/v1/nope", testToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: got %d, want 404 (body %q)", rec.Code, rec.Body.String())
	}
	if env := decodeError(t, rec); env.Error.Code != "not_found" {
		t.Errorf("code %q, want not_found", env.Error.Code)
	}
}

func TestResponsesAreJSONAndNoStore(t *testing.T) {
	h := newTestHandler(t, &fakeQueries{}, nil)
	for _, path := range []string{"/api/v1/overview", "/api/v1/tasks", "/api/v1/graph", "/api/v1/agents", "/api/v1/costs", "/api/v1/settings"} {
		rec := do(t, h, http.MethodGet, path, testToken)
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s content type: got %q, want application/json", path, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s cache control: got %q, want no-store", path, cc)
		}
	}
}

func TestCostsGroupBy(t *testing.T) {
	fq := &fakeQueries{
		costs: func(ctx context.Context, q storage.CostQuery) (*storage.CostReport, error) {
			return &storage.CostReport{GroupBy: "ignored"}, nil
		},
	}
	h := newTestHandler(t, fq, nil)

	rec := do(t, h, http.MethodGet, "/api/v1/costs?group_by=model", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("costs: got %d, want 200", rec.Code)
	}
	if q := fq.recordedCostQuery(); q.GroupBy != "model" {
		t.Errorf("forwarded group_by: got %q, want model", q.GroupBy)
	}
	var report storage.CostReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode costs: %v", err)
	}
	if report.GroupBy != "model" {
		t.Errorf("group_by in body: got %q, want model", report.GroupBy)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/costs?group_by=nonsense", testToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad group_by: got %d, want 400", rec.Code)
	}
	if env := decodeError(t, rec); env.Error.Code != "invalid_parameter" {
		t.Errorf("code %q, want invalid_parameter", env.Error.Code)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	fq := &fakeQueries{
		listTasks: func(ctx context.Context, q storage.TaskQuery) (storage.TaskPage, error) {
			panic("boom")
		},
	}
	h := newTestHandler(t, fq, nil)

	rec := do(t, h, http.MethodGet, "/api/v1/tasks", testToken)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic: got %d, want 500 (body %q)", rec.Code, rec.Body.String())
	}
	if env := decodeError(t, rec); env.Error.Code != "internal_error" {
		t.Errorf("code %q, want internal_error", env.Error.Code)
	}
}

func TestRelativeWindow(t *testing.T) {
	fq := &fakeQueries{}
	h := newTestHandler(t, fq, nil)

	if rec := do(t, h, http.MethodGet, "/api/v1/overview?from=15m", testToken); rec.Code != http.StatusOK {
		t.Fatalf("relative from: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	fq.mu.Lock()
	w := fq.window
	fq.mu.Unlock()
	if want := fixedNow.Add(-15 * time.Minute); !w.From.Equal(want) {
		t.Errorf("from: got %s, want %s", w.From, want)
	}
	if !w.To.Equal(fixedNow) {
		t.Errorf("to: got %s, want %s", w.To, fixedNow)
	}
}
