// Package metrics defines the gateway's Prometheus collectors. The
// management listener exposes them at /metrics; dashboards and platform
// teams consume them (PRD §3.1, §5).
package metrics

import (
	"math"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	dto "github.com/prometheus/client_model/go"
)

// Metrics bundles all gateway instruments; a *Metrics registers itself and
// stays allocation-free on the hot path (labels are strings from small
// enum-like sets).
type Metrics struct {
	registry *prometheus.Registry

	// ProxyRequestsTotal counts proxied A2A requests by agent, inferred
	// binding and status code class.
	ProxyRequestsTotal *prometheus.CounterVec
	// ProxyRequestDurationSeconds is the full proxying time.
	ProxyRequestDurationSeconds *prometheus.HistogramVec
	// ProxyBytesTotal counts proxied payload bytes by direction
	// ("in" = client -> gateway, "out" = gateway -> client).
	ProxyBytesTotal *prometheus.CounterVec
	// ActiveStreams gauges in-flight SSE responses per agent.
	ActiveStreams *prometheus.GaugeVec
	// StreamCapRejectionsTotal counts proxied SSE responses the gateway
	// rejected because limits.max_concurrent_streams was full.
	StreamCapRejectionsTotal *prometheus.CounterVec
	// ActiveStreamsLimit exports the configured stream cap (0=unlimited).
	ActiveStreamsLimit prometheus.Gauge
	// CardServedTotal counts served agent card responses. Its outcome
	// label carries the lookup result (error, fetched, revalidated,
	// stale, cache), so the serving path's cache behaviour stays fully
	// observable with one bounded instrument.
	CardServedTotal *prometheus.CounterVec
	// RegistrySize gauges the number of registered agents.
	RegistrySize prometheus.Gauge
	// AuthFailuresTotal counts rejected caller credentials by reason
	// (missing, invalid).
	AuthFailuresTotal *prometheus.CounterVec
	// TrustVerificationsTotal counts card verification outcomes by status
	// (verified, unverified, unsigned, unchecked, error).
	TrustVerificationsTotal *prometheus.CounterVec
	// PolicyDecisionsTotal counts policy decisions by outcome
	// (allow, warn, deny).
	PolicyDecisionsTotal *prometheus.CounterVec

	// TelemetrySpansEndedTotal counts spans handed to the exporter
	// pipeline, including ones later dropped.
	TelemetrySpansEndedTotal prometheus.Counter
	// TelemetrySpansExportedTotal counts spans successfully exported.
	TelemetrySpansExportedTotal prometheus.Counter
	// TelemetrySpansDroppedTotal counts spans discarded before export,
	// labelled by reason (queue_full, shutdown, export_failed).
	TelemetrySpansDroppedTotal *prometheus.CounterVec
	// TelemetryExportFailuresTotal counts batches that exhausted retries.
	TelemetryExportFailuresTotal prometheus.Counter
	// TelemetryExportRetriesTotal counts failed export attempts that were
	// retried.
	TelemetryExportRetriesTotal prometheus.Counter
	// TelemetryExportDurationSeconds measures one OTLP export attempt.
	TelemetryExportDurationSeconds prometheus.Histogram
	// TelemetryQueueDepth is the current number of spans waiting for export.
	TelemetryQueueDepth prometheus.Gauge
	// TelemetryQueueCapacity is the configured queue bound.
	TelemetryQueueCapacity prometheus.Gauge
}

// New builds and registers the gateway metrics into a fresh registry.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		ProxyRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_proxy_requests_total",
			Help: "Proxied A2A requests by agent, binding and status class.",
		}, []string{"agent", "binding", "code"}),
		ProxyRequestDurationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kyurem_proxy_request_duration_seconds",
			Help:    "Time spent proxying an A2A request (upstream wait included).",
			Buckets: prometheus.ExponentialBuckets(0.0005, 2, 16), // 0.5ms .. ~32s
		}, []string{"agent"}),
		ProxyBytesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_proxy_bytes_total",
			Help: "Proxied bytes: in client->gateway, out gateway->client.",
		}, []string{"agent", "direction"}),
		ActiveStreams: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kyurem_proxy_active_streams",
			Help: "Currently open server-sent-event responses.",
		}, []string{"agent"}),
		StreamCapRejectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_proxy_stream_cap_rejections_total",
			Help: "SSE responses rejected because the agent's stream cap was full.",
		}, []string{"agent"}),
		ActiveStreamsLimit: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kyurem_proxy_active_streams_limit",
			Help: "Configured maximum concurrent streams (0 = unlimited).",
		}),
		CardServedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_card_served_total",
			Help: "Served agent card responses by agent and outcome.",
		}, []string{"agent", "outcome"}),
		RegistrySize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kyurem_registry_agents",
			Help: "Registered agent count.",
		}),
		AuthFailuresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_auth_failures_total",
			Help: "Rejected caller credentials by reason.",
		}, []string{"reason"}),
		TrustVerificationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_trust_verifications_total",
			Help: "Agent card verification outcomes by status.",
		}, []string{"status"}),
		PolicyDecisionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_policy_decisions_total",
			Help: "Authorization decisions by outcome.",
		}, []string{"decision"}),
		TelemetrySpansEndedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kyurem_telemetry_spans_ended_total",
			Help: "Handoff spans ended and handed to the export pipeline.",
		}),
		TelemetrySpansExportedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kyurem_telemetry_spans_exported_total",
			Help: "Handoff spans successfully exported over OTLP.",
		}),
		TelemetrySpansDroppedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_telemetry_spans_dropped_total",
			Help: "Handoff spans dropped before export, by reason.",
		}, []string{"reason"}),
		TelemetryExportFailuresTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kyurem_telemetry_export_failures_total",
			Help: "OTLP batches that failed after all retries.",
		}),
		TelemetryExportRetriesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kyurem_telemetry_export_retries_total",
			Help: "Failed OTLP export attempts that were retried.",
		}),
		TelemetryExportDurationSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "kyurem_telemetry_export_duration_seconds",
			Help:    "Time spent in one OTLP export attempt.",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 12), // 5ms .. ~20s
		}),
		TelemetryQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kyurem_telemetry_queue_depth",
			Help: "Spans currently waiting for OTLP export.",
		}),
		TelemetryQueueCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kyurem_telemetry_queue_capacity",
			Help: "Configured span export queue capacity.",
		}),
	}

	reg.MustRegister(
		m.ProxyRequestsTotal,
		m.ProxyRequestDurationSeconds,
		m.ProxyBytesTotal,
		m.ActiveStreams,
		m.StreamCapRejectionsTotal,
		m.ActiveStreamsLimit,
		m.CardServedTotal,
		m.RegistrySize,
		m.AuthFailuresTotal,
		m.TrustVerificationsTotal,
		m.PolicyDecisionsTotal,
		m.TelemetrySpansEndedTotal,
		m.TelemetrySpansExportedTotal,
		m.TelemetrySpansDroppedTotal,
		m.TelemetryExportFailuresTotal,
		m.TelemetryExportRetriesTotal,
		m.TelemetryExportDurationSeconds,
		m.TelemetryQueueDepth,
		m.TelemetryQueueCapacity,
		// Process- and Go-runtime collectors aid capacity work (PRD §7).
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)
	return m
}

// Registry exposes the underlying registry for the /metrics handler.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// ObserveRequest records timing and outcome of one proxied request.
func (m *Metrics) ObserveRequest(agent, binding string, dur float64, code int) {
	m.ProxyRequestDurationSeconds.WithLabelValues(agent).Observe(dur)
	m.ProxyRequestsTotal.WithLabelValues(agent, binding, codeClass(code)).Inc()
}

// ObserveBytes records proxied payload bytes.
func (m *Metrics) ObserveBytes(agent, direction string, n int64) {
	if n <= 0 {
		return
	}
	m.ProxyBytesTotal.WithLabelValues(agent, direction).Add(float64(n))
}

// ObserveAuthFailure records a rejected caller credential.
func (m *Metrics) ObserveAuthFailure(reason string) {
	if m == nil {
		return
	}
	m.AuthFailuresTotal.WithLabelValues(reason).Inc()
}

// ObserveTrust records one card verification outcome.
func (m *Metrics) ObserveTrust(status string) {
	if m == nil {
		return
	}
	m.TrustVerificationsTotal.WithLabelValues(status).Inc()
}

// ObservePolicyDecision records one authorization decision.
func (m *Metrics) ObservePolicyDecision(decision string) {
	if m == nil {
		return
	}
	m.PolicyDecisionsTotal.WithLabelValues(decision).Inc()
}

// SpanEnded records one ended handoff span entering the export pipeline.
func (m *Metrics) SpanEnded() { m.TelemetrySpansEndedTotal.Inc() }

// SpanDropped records spans discarded before export.
func (m *Metrics) SpanDropped(reason string, n int) {
	if n <= 0 {
		return
	}
	m.TelemetrySpansDroppedTotal.WithLabelValues(reason).Add(float64(n))
}

// TelemetryCounters returns the export-pipeline counters: spans ended,
// spans exported, failed batches, retried attempts and the per-reason
// drop counts. The management API reports them; the Prometheus endpoint
// exports the same numbers.
//
// The values are read from the registered collectors, so the API and
// /metrics can never disagree.
func (m *Metrics) TelemetryCounters() (ended, exported, failures, retries int64, dropped map[string]int64) {
	dropped = map[string]int64{}
	if m == nil {
		return 0, 0, 0, 0, dropped
	}
	ended = int64(counterValue(m.TelemetrySpansEndedTotal))
	exported = int64(counterValue(m.TelemetrySpansExportedTotal))
	failures = int64(counterValue(m.TelemetryExportFailuresTotal))
	retries = int64(counterValue(m.TelemetryExportRetriesTotal))
	for reason, n := range m.droppedCounts() {
		dropped[reason] = n
	}
	return ended, exported, failures, retries, dropped
}

// TelemetryQueue returns the current queue depth and its configured bound.
func (m *Metrics) TelemetryQueue() (depth, capacity int) {
	if m == nil {
		return 0, 0
	}
	return int(gaugeValue(m.TelemetryQueueDepth)), int(gaugeValue(m.TelemetryQueueCapacity))
}

// droppedCounts reads the drop counter vec into a reason -> count map.
func (m *Metrics) droppedCounts() map[string]int64 {
	out := map[string]int64{}
	if m == nil || m.TelemetrySpansDroppedTotal == nil {
		return out
	}
	ch := make(chan prometheus.Metric, 32)
	go func() {
		m.TelemetrySpansDroppedTotal.Collect(ch)
		close(ch)
	}()
	for metric := range ch {
		d := &dto.Metric{}
		if err := metric.Write(d); err != nil {
			continue
		}
		reason := ""
		for _, l := range d.GetLabel() {
			if l.GetName() == "reason" {
				reason = l.GetValue()
			}
		}
		out[reason] = int64(math.Round(d.GetCounter().GetValue()))
	}
	return out
}

// counterValue reads a plain counter through its protobuf representation.
func counterValue(c prometheus.Counter) float64 {
	if c == nil {
		return 0
	}
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// gaugeValue reads a plain gauge.
func gaugeValue(g prometheus.Gauge) float64 {
	if g == nil {
		return 0
	}
	m := &dto.Metric{}
	if err := g.Write(m); err != nil {
		return 0
	}
	return m.GetGauge().GetValue()
}

// SpanExported records spans delivered to the collector.
func (m *Metrics) SpanExported(n int) {
	if n <= 0 {
		return
	}
	m.TelemetrySpansExportedTotal.Add(float64(n))
}

// ExportFailed records a batch that exhausted its retry budget.
func (m *Metrics) ExportFailed(int) { m.TelemetryExportFailuresTotal.Inc() }

// ExportRetried records one failed attempt that will be retried.
func (m *Metrics) ExportRetried() { m.TelemetryExportRetriesTotal.Inc() }

// ObserveExportDuration records the duration of one export attempt.
func (m *Metrics) ObserveExportDuration(seconds float64) {
	m.TelemetryExportDurationSeconds.Observe(seconds)
}

// SetQueueDepth publishes the export queue occupancy.
func (m *Metrics) SetQueueDepth(depth, capacity int) {
	m.TelemetryQueueDepth.Set(float64(depth))
	m.TelemetryQueueCapacity.Set(float64(capacity))
}

// codeClass buckets status codes to bounded label space.
func codeClass(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "other"
	}
}
