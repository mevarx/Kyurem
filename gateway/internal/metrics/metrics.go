// Package metrics defines the gateway's Prometheus collectors. The
// management listener exposes them at /metrics; dashboards and platform
// teams consume them (PRD §3.1, §5).
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
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
	// ActiveStreamsLimit exports the configured stream cap (0=unlimited).
	ActiveStreamsLimit prometheus.Gauge
	// CardLookupsTotal counts agent card cache outcomes.
	CardLookupsTotal *prometheus.CounterVec
	// CardServedTotal counts served agent card responses.
	CardServedTotal *prometheus.CounterVec
	// RegistrySize gauges the number of registered agents.
	RegistrySize prometheus.Gauge

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
		ActiveStreamsLimit: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kyurem_proxy_active_streams_limit",
			Help: "Configured maximum concurrent streams (0 = unlimited).",
		}),
		CardLookupsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_card_lookups_total",
			Help: "Agent card cache outcomes: hit, fetch, stale, error.",
		}, []string{"agent", "result"}),
		CardServedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kyurem_card_served_total",
			Help: "Served agent card responses by agent and outcome.",
		}, []string{"agent", "outcome"}),
		RegistrySize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kyurem_registry_agents",
			Help: "Registered agent count.",
		}),
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
		m.ActiveStreamsLimit,
		m.CardLookupsTotal,
		m.CardServedTotal,
		m.RegistrySize,
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

// SpanEnded records one ended handoff span entering the export pipeline.
func (m *Metrics) SpanEnded() { m.TelemetrySpansEndedTotal.Inc() }

// SpanDropped records spans discarded before export.
func (m *Metrics) SpanDropped(reason string, n int) {
	if n <= 0 {
		return
	}
	m.TelemetrySpansDroppedTotal.WithLabelValues(reason).Add(float64(n))
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
