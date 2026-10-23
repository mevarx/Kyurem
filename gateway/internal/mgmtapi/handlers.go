// Package mgmtapi implements the read-only Kyurem management API (PRD §4.6).
//
// The API lives on its own listener, separate from the A2A data path, and is
// authenticated with a bearer token. It reads the same spans the OTel
// collector exports, so the dashboard shows exactly what the trace backend
// shows.
//
// Rules that hold for every endpoint:
//   - responses are JSON and carry "Cache-Control: no-store";
//   - errors use the {"error":{"code","message"}} envelope;
//   - internal failures are logged, never leaked to the client;
//   - a panic in any handler answers 500 with the same envelope.
package mgmtapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sort"
	"time"

	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
	"github.com/kyurem-project/kyurem/gateway/internal/policy"
	"github.com/kyurem-project/kyurem/gateway/internal/registry"
	"github.com/kyurem-project/kyurem/gateway/internal/storage"
	"github.com/kyurem-project/kyurem/gateway/internal/telemetry"
	"github.com/kyurem-project/kyurem/gateway/internal/trust"
	"github.com/kyurem-project/kyurem/gateway/internal/version"
)

// defaultServiceName is reported by /settings when no telemetry
// configuration is available (the gateway's own default service name).
const defaultServiceName = "kyurem-gateway"

// Deps carries everything the management API reads.
type Deps struct {
	Queries  storage.Queries
	Registry *registry.Registry
	Cards    *registry.CardCache // may be nil
	Metrics  *metrics.Metrics    // may be nil
	// Telemetry may be nil.
	Telemetry *telemetry.Telemetry
	// Policy is the authorization engine (M5). A nil engine reports an
	// empty, disabled policy.
	Policy *policy.Engine
	// Audit is the process-local decision ring (M5). May be nil.
	Audit *policy.AuditLog
	// Trust is the card verifier; its per-agent mode is reported even
	// before the first card snapshot exists. May be nil.
	Trust *trust.Verifier
	// StorageDriver is "memory" or "clickhouse" (reported by /settings).
	StorageDriver   string
	StorageDatabase string
	// AuthToken is the bearer token required by the API. When empty the
	// API is unauthenticated (only acceptable on a loopback listener).
	AuthToken string
	Logger    *slog.Logger
	// Now is the clock (tests inject a fixed one). Defaults to time.Now.
	Now func() time.Time
	// Uptime is the process start time used for the uptime_seconds field.
	Uptime time.Time
}

// server holds the normalized dependencies and the handlers.
type server struct {
	deps Deps
}

// New returns the management API handler.
//
// The returned handler is an *http.ServeMux carrying the documented routes.
// Every route is wrapped in a panic-recovery middleware, so a bug in one
// handler cannot take the management listener down.
func New(deps Deps) http.Handler {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	s := &server{deps: deps}
	if deps.AuthToken == "" {
		deps.Logger.Warn("management API is unauthenticated: set management.auth_token before exposing it beyond loopback")
	}

	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/overview", s.safe(s.authed(http.HandlerFunc(s.handleOverview))))
	mux.Handle("GET /api/v1/tasks", s.safe(s.authed(http.HandlerFunc(s.handleTasks))))
	mux.Handle("GET /api/v1/tasks/{task_id}", s.safe(s.authed(http.HandlerFunc(s.handleTaskDetail))))
	mux.Handle("GET /api/v1/graph", s.safe(s.authed(http.HandlerFunc(s.handleGraph))))
	mux.Handle("GET /api/v1/agents", s.safe(s.authed(http.HandlerFunc(s.handleAgents))))
	mux.Handle("GET /api/v1/costs", s.safe(s.authed(http.HandlerFunc(s.handleCosts))))
	mux.Handle("GET /api/v1/policy", s.safe(s.authed(http.HandlerFunc(s.handlePolicy))))
	mux.Handle("GET /api/v1/decisions", s.safe(s.authed(http.HandlerFunc(s.handleDecisions))))
	mux.Handle("GET /api/v1/settings", s.safe(s.authed(http.HandlerFunc(s.handleSettings))))
	// Probes are unauthenticated: orchestrators cannot hold a token.
	mux.Handle("GET /healthz", s.safe(http.HandlerFunc(s.handleHealthz)))
	mux.Handle("GET /readyz", s.safe(http.HandlerFunc(s.handleReadyz)))
	// Everything else is a 404 with the standard error envelope.
	mux.Handle("/", s.safe(http.HandlerFunc(s.handleNotFound)))
	return mux
}

// ---------------------------------------------------------------- responses

// errorEnvelope is the error shape of api/openapi.yaml.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON renders v as the response body. The content type and the
// no-store cache policy are set for every response, success or not.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already out; nothing left but a log.
		slog.Default().Error("management API write failed", "err", err)
	}
}

// writeError answers with the standard error envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

// internalError logs the cause and answers with a generic message: the
// client learns that the gateway failed, never why.
func (s *server) internalError(w http.ResponseWriter, msg string, err error, attrs ...any) {
	if s.deps.Logger != nil {
		s.deps.Logger.Error(msg, append([]any{"err", err}, attrs...)...)
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
}

// safe wraps a handler with panic recovery.
func (s *server) safe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if s.deps.Logger != nil {
					s.deps.Logger.Error("management API panic",
						"path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				}
				writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// handleNotFound answers unknown paths.
func (s *server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "not found")
}

// ------------------------------------------------------------------- probes

type healthResponse struct {
	Status string `json:"status"`
	// Agents is reported by /readyz only.
	Agents *int `json:"agents,omitempty"`
}

// handleHealthz is the liveness probe.
func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// handleReadyz is the readiness probe; it also reports how many agents are
// registered.
func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	n := s.agentCount()
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Agents: &n})
}

// agentCount is the number of registered agents (0 without a registry).
func (s *server) agentCount() int {
	if s.deps.Registry == nil {
		return 0
	}
	return s.deps.Registry.Len()
}

// ----------------------------------------------------------------- handlers

// handleOverview serves the dashboard summary for a window.
func (s *server) handleOverview(w http.ResponseWriter, r *http.Request) {
	win, err := s.parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameter", err.Error())
		return
	}
	if s.deps.Queries == nil {
		s.internalError(w, "overview query unavailable", errors.New("no query layer configured"))
		return
	}
	ov, err := s.deps.Queries.Overview(r.Context(), win)
	if err != nil {
		s.internalError(w, "overview query failed", err)
		return
	}
	if ov == nil {
		ov = &storage.Overview{}
	}
	if ov.TasksOverTime == nil {
		ov.TasksOverTime = []storage.TimeBucket{}
	}
	if ov.CostOverTime == nil {
		ov.CostOverTime = []storage.TimeBucket{}
	}
	if ov.TopAgentsByCost == nil {
		ov.TopAgentsByCost = []storage.CostBucket{}
	}
	if ov.TopFailing == nil {
		ov.TopFailing = []storage.FailingAgent{}
	}
	if ov.RecentFailed == nil {
		ov.RecentFailed = []storage.Task{}
	}
	writeJSON(w, http.StatusOK, ov)
}

// handleTasks serves the filtered, paginated task list.
func (s *server) handleTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	win, err := s.parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameter", err.Error())
		return
	}
	tq := storage.TaskQuery{
		Window: win,
		Status: q.Get("status"),
		Agent:  q.Get("agent"),
		TaskID: q.Get("task_id"),
	}
	if tq.Status != "" && !validStatuses[tq.Status] {
		writeError(w, http.StatusBadRequest, "invalid_parameter",
			"invalid status "+q.Get("status")+": want succeeded, failed or unknown")
		return
	}
	if raw := q.Get("min_cost_usd"); raw != "" {
		v, err := parseFloat(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "invalid min_cost_usd: "+raw)
			return
		}
		tq.MinCostUSD = &v
	}
	if raw := q.Get("max_cost_usd"); raw != "" {
		v, err := parseFloat(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "invalid max_cost_usd: "+raw)
			return
		}
		tq.MaxCostUSD = &v
	}
	limit, err := parseInt(q.Get("limit"), DefaultLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameter", "invalid limit: "+q.Get("limit"))
		return
	}
	tq.Limit = clampLimit(limit)
	offset, err := parseInt(q.Get("offset"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameter", "invalid offset: "+q.Get("offset"))
		return
	}
	if offset < 0 {
		offset = 0
	}
	tq.Offset = offset

	if s.deps.Queries == nil {
		s.internalError(w, "task query unavailable", errors.New("no query layer configured"))
		return
	}
	page, err := s.deps.Queries.ListTasks(r.Context(), tq)
	if err != nil {
		s.internalError(w, "list tasks failed", err)
		return
	}
	if page.Items == nil {
		page.Items = []storage.Task{}
	}
	writeJSON(w, http.StatusOK, page)
}

// handleTaskDetail serves one task: span tree, root-cause hint, cost.
func (s *server) handleTaskDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("task_id")
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	if s.deps.Queries == nil {
		s.internalError(w, "task detail unavailable", errors.New("no query layer configured"))
		return
	}
	detail, err := s.deps.Queries.TaskDetail(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "task not found")
			return
		}
		s.internalError(w, "task detail failed", err, "task_id", id)
		return
	}
	if detail == nil {
		writeError(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	if detail.Spans == nil {
		detail.Spans = []storage.Span{}
	}
	if detail.Events == nil {
		detail.Events = []storage.Event{}
	}
	if detail.Cost.Buckets == nil {
		detail.Cost.Buckets = []storage.CostBucket{}
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleGraph serves the agent-to-agent call graph for a window.
func (s *server) handleGraph(w http.ResponseWriter, r *http.Request) {
	win, err := s.parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameter", err.Error())
		return
	}
	if s.deps.Queries == nil {
		s.internalError(w, "graph query unavailable", errors.New("no query layer configured"))
		return
	}
	g, err := s.deps.Queries.Graph(r.Context(), win)
	if err != nil {
		s.internalError(w, "graph query failed", err)
		return
	}
	if g == nil {
		g = &storage.Graph{}
	}
	if g.Nodes == nil {
		g.Nodes = []storage.GraphNode{}
	}
	if g.Edges == nil {
		g.Edges = []storage.GraphEdge{}
	}
	writeJSON(w, http.StatusOK, g)
}

// handleAgents merges the registry (who is configured) with the span-derived
// statistics (what actually happened) and the card cache (how fresh the
// upstream card is).
func (s *server) handleAgents(w http.ResponseWriter, r *http.Request) {
	win, err := s.parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameter", err.Error())
		return
	}
	stats := map[string]storage.AgentStats{}
	if s.deps.Queries != nil {
		list, err := s.deps.Queries.AgentStats(r.Context(), win)
		if err != nil {
			s.internalError(w, "agent stats failed", err)
			return
		}
		for _, st := range list {
			stats[st.Name] = st
		}
	}
	items := []AgentInfo{}
	if s.deps.Registry != nil {
		for _, e := range s.deps.Registry.Entries() {
			if e == nil {
				continue
			}
			items = append(items, s.agentInfo(e, stats[e.Name]))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	writeJSON(w, http.StatusOK, agentsResponse{Items: items})
}

// handleCosts serves the cost and token roll-up.
func (s *server) handleCosts(w http.ResponseWriter, r *http.Request) {
	win, err := s.parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameter", err.Error())
		return
	}
	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = DefaultGroupBy
	}
	if !validGroupBys[groupBy] {
		writeError(w, http.StatusBadRequest, "invalid_parameter",
			"invalid group_by "+groupBy+": want agent, model, task or window")
		return
	}
	if s.deps.Queries == nil {
		s.internalError(w, "cost query unavailable", errors.New("no query layer configured"))
		return
	}
	report, err := s.deps.Queries.Costs(r.Context(), storage.CostQuery{Window: win, GroupBy: groupBy})
	if err != nil {
		s.internalError(w, "cost query failed", err)
		return
	}
	if report == nil {
		report = &storage.CostReport{}
	}
	// The report always describes the grouping that was asked for.
	report.GroupBy = groupBy
	if report.Buckets == nil {
		report.Buckets = []storage.CostBucket{}
	}
	writeJSON(w, http.StatusOK, report)
}

// handlePolicy serves the configured policy and the caller x callee matrix.
func (s *server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	snap := s.deps.Policy.Snapshot()
	if snap.Rules == nil {
		snap.Rules = []policy.Rule{}
	}
	if snap.Callers == nil {
		snap.Callers = []string{}
	}
	if snap.Callees == nil {
		snap.Callees = []string{}
	}
	if snap.Matrix == nil {
		snap.Matrix = []policy.MatrixCell{}
	}
	writeJSON(w, http.StatusOK, snap)
}

// decisionsResponse is the /decisions payload.
type decisionsResponse struct {
	Items []policy.AuditEntry `json:"items"`
	// Total is the number of entries currently held by the ring.
	Total int `json:"total"`
}

// handleDecisions serves the in-process audit ring, newest first.
func (s *server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "" && !validDecisionKinds[kind] {
		writeError(w, http.StatusBadRequest, "invalid_parameter",
			"invalid kind "+kind+": want policy, trust or auth")
		return
	}
	limit, err := parseInt(q.Get("limit"), DefaultDecisionsLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameter", "invalid limit: "+q.Get("limit"))
		return
	}
	limit = min(max(limit, MinLimit), MaxDecisionsLimit)
	items := s.deps.Audit.Recent(kind, limit)
	if items == nil {
		items = []policy.AuditEntry{}
	}
	total := 0
	if s.deps.Audit != nil {
		total = s.deps.Audit.Len()
	}
	writeJSON(w, http.StatusOK, decisionsResponse{Items: items, Total: total})
}

// ---------------------------------------------------------------- settings

// settingsResponse is the /settings payload (see api/openapi.yaml).
type settingsResponse struct {
	Version         string            `json:"version"`
	SpecVersion     string            `json:"spec_version"`
	A2AGoSDKVersion string            `json:"a2a_go_sdk_version"`
	ServiceName     string            `json:"service_name"`
	UptimeSeconds   float64           `json:"uptime_seconds"`
	Agents          int               `json:"agents"`
	Telemetry       telemetrySettings `json:"telemetry"`
	Storage         storageSettings   `json:"storage"`
}

type telemetrySettings struct {
	Exporting      bool             `json:"exporting"`
	OTLPEndpoint   string           `json:"otlp_endpoint"`
	Protocol       string           `json:"protocol"`
	SampleRatio    float64          `json:"sample_ratio"`
	QueueDepth     int              `json:"queue_depth"`
	QueueCapacity  int              `json:"queue_capacity"`
	SpansEnded     int64            `json:"spans_ended"`
	SpansExported  int64            `json:"spans_exported"`
	SpansDropped   map[string]int64 `json:"spans_dropped"`
	ExportFailures int64            `json:"export_failures"`
	ExportRetries  int64            `json:"export_retries"`
}

type storageSettings struct {
	Driver   string `json:"driver"`
	Database string `json:"database"`
}

// handleSettings reports the gateway status: versions, uptime, agent count
// and the telemetry counters.
func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	now := s.deps.Now()
	resp := settingsResponse{
		Version:         version.Version,
		SpecVersion:     version.SpecVersion,
		A2AGoSDKVersion: version.GoVersion(),
		ServiceName:     defaultServiceName,
		Agents:          s.agentCount(),
		Storage: storageSettings{
			Driver:   s.deps.StorageDriver,
			Database: s.deps.StorageDatabase,
		},
		Telemetry: s.telemetrySettings(),
	}
	if tel := s.deps.Telemetry; tel != nil {
		if name := tel.Config().ServiceName; name != "" {
			resp.ServiceName = name
		}
	}
	if !s.deps.Uptime.IsZero() {
		resp.UptimeSeconds = now.Sub(s.deps.Uptime).Seconds()
	}
	writeJSON(w, http.StatusOK, resp)
}

// telemetrySettings reads the telemetry counters. Every dependency is
// optional: a gateway without metrics or telemetry still answers.
func (s *server) telemetrySettings() telemetrySettings {
	ts := telemetrySettings{
		SampleRatio:  1,
		SpansDropped: map[string]int64{},
	}
	if tel := s.deps.Telemetry; tel != nil {
		ts.Exporting = tel.Exporting()
		cfg := tel.Config()
		ts.OTLPEndpoint = cfg.OTLPEndpoint
		ts.Protocol = cfg.OTLPProtocol
		ts.SampleRatio = cfg.EffectiveSampleRatio()
	}
	if m := s.deps.Metrics; m != nil {
		ts.QueueDepth, ts.QueueCapacity = m.TelemetryQueue()
		ts.SpansEnded, ts.SpansExported, ts.ExportFailures, ts.ExportRetries, ts.SpansDropped = m.TelemetryCounters()
	}
	return ts
}

// ------------------------------------------------------------------ agents

// agentsResponse is the /agents payload.
type agentsResponse struct {
	Items []AgentInfo `json:"items"`
}

// AgentInfo is one registered agent with its card state and its traffic
// statistics (see the AgentInfo schema in api/openapi.yaml).
type AgentInfo struct {
	Name      string   `json:"name"`
	Aliases   []string `json:"aliases"`
	Tags      []string `json:"tags"`
	Path      string   `json:"path"`
	Upstreams []string `json:"upstreams"`
	// CardStatus is "ok", "stale", "error" or "unknown".
	CardStatus string `json:"card_status"`
	// CardFetchedAt is null when no card was ever fetched.
	CardFetchedAt *time.Time `json:"card_fetched_at"`
	// CardVersion is the version the upstream card declares.
	CardVersion string `json:"card_version"`
	// Skills lists the skill ids the upstream card declares.
	Skills []string `json:"skills"`
	// Verification is the card signature status: checked, verified,
	// unverified, unsigned or unchecked.
	Verification string `json:"verification"`
	// VerificationMode is the configured trust mode: off, warn or enforce.
	VerificationMode string `json:"verification_mode"`
	// VerificationKey is the kid that verified the signature, when any.
	VerificationKey string `json:"verification_key,omitempty"`
	// VerificationReason explains the status in one sentence.
	VerificationReason string `json:"verification_reason,omitempty"`
	// BreakerState is "unknown" until the circuit breaker lands (M6).
	BreakerState string     `json:"breaker_state"`
	LastSeen     *time.Time `json:"last_seen"`
	Spans        int        `json:"spans"`
	Errors       int        `json:"errors"`
	ErrorRate    float64    `json:"error_rate"`
	P50MS        float64    `json:"p50_ms"`
	P95MS        float64    `json:"p95_ms"`
	CostUSD      *float64   `json:"cost_usd"`
}

// agentInfo merges one registry entry with its statistics and its card
// snapshot.
func (s *server) agentInfo(e *registry.Entry, st storage.AgentStats) AgentInfo {
	info := AgentInfo{
		Name:             e.Name,
		Aliases:          []string{},
		Tags:             []string{},
		Path:             e.RoutePath,
		Upstreams:        []string{},
		CardStatus:       "unknown",
		Skills:           []string{},
		Verification:     trust.StatusUnchecked,
		VerificationMode: string(trust.ModeOff),
		BreakerState:     "unknown",
		LastSeen:         st.LastSeen,
		Spans:            st.Spans,
		Errors:           st.Errors,
		ErrorRate:        st.ErrorRate,
		P50MS:            st.P50MS,
		P95MS:            st.P95MS,
		CostUSD:          st.CostUSD,
	}
	if s.deps.Trust != nil {
		info.VerificationMode = string(s.deps.Trust.Mode(e.Name))
	}
	for _, alias := range e.AliasPaths {
		info.Aliases = append(info.Aliases, aliasSegment(alias))
	}
	info.Tags = append(info.Tags, e.Tags...)
	for _, u := range e.Upstreams {
		info.Upstreams = append(info.Upstreams, u.URL)
	}
	s.applyCardSnapshot(&info, e)
	return info
}

// applyCardSnapshot fills the card fields from the cache. Peek never
// triggers a fetch: the management API reports what the cache knows.
//
// The cache does not expose its TTL, so freshness is derived from the
// snapshot's own state: a snapshot that failed to refresh (LastErr,
// FailedRefresh) or was served stale is reported as "stale", a snapshot
// without a usable card but with an error as "error", and a healthy
// snapshot as "ok".
func (s *server) applyCardSnapshot(info *AgentInfo, e *registry.Entry) {
	if s.deps.Cards == nil {
		return
	}
	snap := s.deps.Cards.Peek(e)
	if snap == nil {
		return
	}
	if snap.Card != nil {
		info.CardVersion = snap.Card.Version
		for _, skill := range snap.Card.Skills {
			if skill.ID != "" {
				info.Skills = append(info.Skills, skill.ID)
			}
		}
	}
	verification := snap.Verification
	if verification.Mode != "" {
		info.VerificationMode = verification.Mode
	}
	if verification.Status != "" {
		info.Verification = verification.Status
	}
	info.VerificationKey = verification.KeyID
	info.VerificationReason = verification.Reason
	if !snap.FetchedAt.IsZero() {
		fetched := snap.FetchedAt
		info.CardFetchedAt = &fetched
	}
	switch {
	case snap.Card == nil && snap.LastErr != nil:
		info.CardStatus = "error"
	case snap.Card == nil:
		info.CardStatus = "unknown"
	case snap.LastErr != nil || snap.FailedRefresh || snap.Source == "stale":
		info.CardStatus = "stale"
	default:
		info.CardStatus = "ok"
	}
}
