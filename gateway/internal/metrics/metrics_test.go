// Package metrics contract tests: every registered series is documented
// in a name -> purpose table, registration keeps rejecting name
// collisions (no aliases), and the programmatic reader helpers agree with
// what a /metrics scrape reports.
package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// metricContract maps every kyurem_* series the gateway documents to its
// purpose. An instrument that is registered without an entry here fails
// the inventory test, and so does an entry that never produces a series:
// the table is the contract the README and the PRD observability promises
// (request counts, latency histograms, active streams, queue depth,
// dropped spans) link to.
var metricContract = map[string]string{
	"kyurem_proxy_requests_total":              "Proxied A2A requests by agent, binding and status code class.",
	"kyurem_proxy_request_duration_seconds":    "Latency histogram of the full proxying time per agent.",
	"kyurem_proxy_bytes_total":                 "Proxied payload bytes per agent and direction (in/out).",
	"kyurem_proxy_active_streams":              "Currently open SSE responses per agent.",
	"kyurem_proxy_stream_cap_rejections_total": "Proxied SSE responses rejected because the agent's stream cap was full.",
	"kyurem_proxy_active_streams_limit":        "Configured maximum concurrent streams (0 = unlimited).",
	"kyurem_card_served_total":                 "Served agent cards per agent and outcome (fetched/revalidated/stale/cache/error).",
	"kyurem_registry_agents":                   "Registered agent count.",
	"kyurem_telemetry_spans_ended_total":       "Handoff spans ended and handed to the export pipeline.",
	"kyurem_telemetry_spans_exported_total":    "Handoff spans successfully exported over OTLP.",
	"kyurem_telemetry_spans_dropped_total":     "Spans dropped before export, by reason (queue_full/shutdown/export_failed).",
	"kyurem_telemetry_export_failures_total":   "OTLP batches that failed after their retry budget.",
	"kyurem_telemetry_export_retries_total":    "Failed OTLP export attempts that were retried.",
	"kyurem_telemetry_export_duration_seconds": "Duration of one OTLP export attempt.",
	"kyurem_telemetry_queue_depth":             "Spans currently waiting for OTLP export.",
	"kyurem_telemetry_queue_capacity":          "Configured span export queue capacity.",
}

// bundledCollectorPrefixes names the client_golang built-in collectors the
// gateway registers for capacity work (PRD §7). Their name sets are owned
// by client_golang and grow with its versions, so the contract for them is
// a prefix, deliberately weaker than the exact kyurem_ contract.
var bundledCollectorPrefixes = map[string]string{
	"go_":      "Go runtime collector: goroutines, threads, memory statistics.",
	"process_": "Process collector: CPU, file descriptors, memory, start time.",
}

// touchAll exercises every instrument through the recording helpers the
// pipelines actually use, so each registered collector produces at least
// one series and shows up in a scrape.
func touchAll(m *Metrics) {
	m.ObserveRequest("probe", "JSONRPC", 0.01, 200)
	m.ObserveBytes("probe", "in", 12)
	m.ObserveBytes("probe", "out", 34)
	m.ActiveStreams.WithLabelValues("probe").Inc()
	m.StreamCapRejectionsTotal.WithLabelValues("probe").Inc()
	m.ActiveStreamsLimit.Set(8)
	m.CardServedTotal.WithLabelValues("probe", "fetched").Inc()
	m.RegistrySize.Set(1)
	m.SpanEnded()
	m.SpanExported(1)
	m.SpanDropped("queue_full", 1)
	m.SpanDropped("shutdown", 1)
	m.SpanDropped("export_failed", 1)
	m.ExportFailed(1)
	m.ExportRetried()
	m.ObserveExportDuration(0.005)
	m.SetQueueDepth(3, 2048)
}

// TestMetricInventory enforces the scraping contract: every gathered
// family is either a documented kyurem_ metric or a bundled collector, and
// every documented kyurem_ metric is actually live.
func TestMetricInventory(t *testing.T) {
	m := New()
	touchAll(m)

	families, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	seen := make(map[string]bool, len(families))
	for _, fam := range families {
		name := fam.GetName()
		seen[name] = true
		if purpose, ok := metricContract[name]; ok {
			if purpose == "" {
				t.Errorf("metric %q has an empty purpose", name)
			}
			continue
		}
		allowedPrefix := false
		for prefix := range bundledCollectorPrefixes {
			if strings.HasPrefix(name, prefix) {
				allowedPrefix = true
				break
			}
		}
		if !allowedPrefix {
			t.Errorf("unexpected metric family %q: either wire it from real code and add it to "+
				"metricContract, or remove it (no decorative instruments)", name)
		}
	}

	for name := range metricContract {
		if !seen[name] {
			t.Errorf("contract metric %q produced no series: touch it in touchAll or remove it from the contract", name)
		}
	}
}

// TestDuplicateRegistrationPanics documents the no-alias guarantee:
// MustRegister rejects a second collector claiming an existing name, so
// MetricInventory can never see two instruments under one family.
func TestDuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("duplicate registration must panic instead of aliasing")
		}
	}()
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{Name: "kyurem_test_probe_total"}))
	reg.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{Name: "kyurem_test_probe_total", Help: "second"}))
}

func TestCodeClass(t *testing.T) {
	cases := map[int]string{
		-1:   "other",
		0:    "other",
		99:   "other",
		100:  "1xx",
		199:  "1xx",
		200:  "2xx",
		204:  "2xx",
		299:  "2xx",
		302:  "3xx",
		399:  "3xx",
		400:  "4xx",
		499:  "4xx",
		500:  "5xx",
		599:  "5xx",
		600:  "other",
		1300: "other",
	}
	for code, want := range cases {
		if got := codeClass(code); got != want {
			t.Errorf("codeClass(%d) = %q, want %q", code, got, want)
		}
	}
}

// TestTelemetryCountersMatchScrape keeps the management API numbers and
// the /metrics endpoint consistent: TelemetryCounters and TelemetryQueue
// read the registered collectors, so both surfaces must agree with a raw
// scrape, label for label.
func TestTelemetryCountersMatchScrape(t *testing.T) {
	m := New()
	m.SpanEnded()
	m.SpanEnded()
	m.SpanExported(2)
	m.SpanDropped("queue_full", 2)
	m.SpanDropped("shutdown", 1)
	m.ExportFailed(1)
	m.ExportRetried()
	m.SetQueueDepth(7, 2048)

	ended, exported, failures, retries, dropped := m.TelemetryCounters()
	if ended != 2 || exported != 2 {
		t.Errorf("TelemetryCounters want ended=2 exported=2, got ended=%d exported=%d", ended, exported)
	}
	if failures != 1 || retries != 1 {
		t.Errorf("TelemetryCounters want failures=1 retries=1, got failures=%d retries=%d", failures, retries)
	}
	if len(dropped) != 2 || dropped["queue_full"] != 2 || dropped["shutdown"] != 1 {
		t.Errorf("drop counts mismatch: %v", dropped)
	}
	if depth, capacity := m.TelemetryQueue(); depth != 7 || capacity != 2048 {
		t.Errorf("TelemetryQueue want 7/2048, got %d/%d", depth, capacity)
	}

	// Cross-check against a raw scrape (the /metrics surface).
	fams, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	byName := make(map[string]*dto.MetricFamily, len(fams))
	for _, fam := range fams {
		byName[fam.GetName()] = fam
	}

	if got := familyCounterValue(byName, "kyurem_telemetry_spans_ended_total"); got != float64(ended) {
		t.Errorf("scrape ended = %v, TelemetryCounters says %d", got, ended)
	}
	if got := familyCounterValue(byName, "kyurem_telemetry_spans_exported_total"); got != float64(exported) {
		t.Errorf("scrape exported = %v, TelemetryCounters says %d", got, exported)
	}
	if got := familyCounterValue(byName, "kyurem_telemetry_export_failures_total"); got != float64(failures) {
		t.Errorf("scrape failures = %v, TelemetryCounters says %d", got, failures)
	}
	if got := familyCounterValue(byName, "kyurem_telemetry_export_retries_total"); got != float64(retries) {
		t.Errorf("scrape retries = %v, TelemetryCounters says %d", got, retries)
	}
	scrapeDropped := map[string]float64{}
	for _, metric := range byName["kyurem_telemetry_spans_dropped_total"].GetMetric() {
		for _, l := range metric.GetLabel() {
			if l.GetName() == "reason" {
				scrapeDropped[l.GetValue()] = metric.GetCounter().GetValue()
			}
		}
	}
	if len(scrapeDropped) != len(dropped) {
		t.Errorf("scrape drop labels %v do not match counter map %v", scrapeDropped, dropped)
	}
	for reason, want := range dropped {
		if got := scrapeDropped[reason]; got != float64(want) {
			t.Errorf("scrape drop[%s] = %v, TelemetryCounters says %d", reason, got, want)
		}
	}

	depthFam := byName["kyurem_telemetry_queue_depth"]
	capacityFam := byName["kyurem_telemetry_queue_capacity"]
	if got := depthFam.GetMetric()[0].GetGauge().GetValue(); got != float64(7) {
		t.Errorf("scrape queue depth = %v, want 7", got)
	}
	if got := capacityFam.GetMetric()[0].GetGauge().GetValue(); got != float64(2048) {
		t.Errorf("scrape queue capacity = %v, want 2048", got)
	}

	// The direct collector reads must agree too (testutil reads through
	// the same Write path /metrics uses).
	if got := testutil.ToFloat64(m.TelemetrySpansEndedTotal); got != float64(ended) {
		t.Errorf("testutil ended = %v, TelemetryCounters says %d", got, ended)
	}
}

// TestTelemetryCountersNilSafe covers the nil-receiver guard the management
// API relies on when a gateway runs without metrics.
func TestTelemetryCountersNilSafe(t *testing.T) {
	var m *Metrics
	ended, exported, failures, retries, dropped := m.TelemetryCounters()
	if ended != 0 || exported != 0 || failures != 0 || retries != 0 || len(dropped) != 0 {
		t.Errorf("nil Metrics must report zeros, got ended=%d exported=%d failures=%d retries=%d dropped=%v",
			ended, exported, failures, retries, dropped)
	}
	if depth, capacity := m.TelemetryQueue(); depth != 0 || capacity != 0 {
		t.Errorf("nil Metrics must report a 0/0 queue, got %d/%d", depth, capacity)
	}
}

// familyCounterValue reads the single counter of a family (all counters
// exercised by touchAll have exactly one series).
func familyCounterValue(byName map[string]*dto.MetricFamily, name string) float64 {
	fam, ok := byName[name]
	if !ok {
		return -1
	}
	metrics := fam.GetMetric()
	if len(metrics) == 0 {
		return -1
	}
	return metrics[0].GetCounter().GetValue()
}
