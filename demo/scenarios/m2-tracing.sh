#!/usr/bin/env bash
#
# Kyurem M2 end-to-end demo ("one task is one trace tree").
#
# Brings up the compose stack (gateway -> OTel collector -> ClickHouse,
# plus Prometheus and Grafana), sends one chained A2A task through the
# gateway (client -> gateway -> upper -> gateway -> echo) and asserts that
# the resulting spans form a single trace tree with the expected
# attributes:
#
#   1. at least two handoff spans share one trace ID
#   2. exactly one root span, the echo span is a child of the upper span
#   3. the root hop carries the task id and the final task status
#   4. both hops carry the same A2A context id
#
# Requirements: docker with the compose v2 plugin. No Go toolchain needed.
#
# NOTE: this script requires a working Docker installation. It was not
# executed in the environment where M2 was implemented (Docker was
# unavailable there); the Go-side behavior is covered by the M2 integration
# tests in gateway/test/integration/m2_test.go.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

COMPOSE_FILE="deploy/compose/docker-compose.yml"
COMPOSE=(docker compose -f "$COMPOSE_FILE")
CH_PASSWORD="${CLICKHOUSE_PASSWORD:-kyurem-demo}"
TASK_MESSAGE="kyurem m2 trace tree"

PASS=0
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { PASS=$((PASS + 1)); echo "PASS: $*"; }

if ! docker compose version >/dev/null 2>&1; then
  fail "docker compose v2 is required (https://docs.docker.com/compose/install/)"
fi

query() { # sql
  "${COMPOSE[@]}" exec -T clickhouse \
    clickhouse-client --user default --password "$CH_PASSWORD" \
    --query "$1" 2>/dev/null | tr -d '\r'
}

echo "== starting the compose stack =="
"${COMPOSE[@]}" up -d --build

echo "== waiting for the gateway =="
for _ in $(seq 1 60); do
  if "${COMPOSE[@]}" exec -T gateway wget -qO- http://127.0.0.1:8081/healthz >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
"${COMPOSE[@]}" exec -T gateway wget -qO- http://127.0.0.1:8081/healthz >/dev/null 2>&1 \
  || fail "gateway did not become healthy"

echo "== sending one chained task through the gateway =="
out="$("${COMPOSE[@]}" run --rm --no-deps demo-client \
  --gateway-url http://gateway:8080/a2a/upper --message "$TASK_MESSAGE" --stream 2>&1)" \
  || { echo "$out"; fail "demo client failed"; }
echo "$out" | grep -q 'upper: KYUREM M2 TRACE TREE' \
  && pass "chained task completed" || { echo "$out"; fail "unexpected task result"; }

echo "== waiting for the spans to reach ClickHouse =="
spans=""
for _ in $(seq 1 30); do
  spans="$(query "SELECT count() FROM otel.otel_traces")"
  [ "${spans:-0}" -ge 2 ] && break
  sleep 2
done
[ "${spans:-0}" -ge 2 ] || fail "expected at least 2 spans in ClickHouse, got ${spans:-0}"
pass "$spans spans stored in ClickHouse"

trace_id="$(query "SELECT TraceId FROM otel.otel_traces ORDER BY Timestamp DESC LIMIT 1")"
[ -n "$trace_id" ] || fail "no trace id found"

span_count="$(query "SELECT count() FROM otel.otel_traces WHERE TraceId = '$trace_id'")"
roots="$(query "SELECT count() FROM otel.otel_traces WHERE TraceId = '$trace_id' AND ParentSpanId = ''")"
[ "$span_count" -ge 2 ] \
  && pass "trace $trace_id has $span_count handoff spans" \
  || fail "trace $trace_id has $span_count spans, want at least 2"
[ "$roots" = "1" ] \
  && pass "trace $trace_id has exactly one root span" \
  || fail "trace $trace_id has $roots roots, want 1"

echo
echo "-- trace tree --"
query "
    SELECT
        SpanName AS span,
        if(ParentSpanId = '', 'root', 'child') AS kind,
        SpanAttributes['kyurem.caller.agent'] AS caller,
        SpanAttributes['kyurem.callee.agent'] AS callee,
        SpanAttributes['kyurem.task.id'] AS task_id,
        SpanAttributes['kyurem.context.id'] AS context_id,
        SpanAttributes['kyurem.task.status'] AS task_status,
        round(Duration / 1e6, 1) AS latency_ms
    FROM otel.otel_traces
    WHERE TraceId = '$trace_id'
    ORDER BY Timestamp ASC
    FORMAT PrettyCompact"

tasks="$(query "SELECT count() FROM otel.otel_traces WHERE TraceId = '$trace_id' AND ParentSpanId = '' AND SpanAttributes['kyurem.task.id'] != ''")"
[ "$tasks" = "1" ] \
  && pass "the root hop carries the task id" \
  || fail "expected one root hop with a task id, got $tasks"

status="$(query "SELECT countDistinct(SpanAttributes['kyurem.task.status']) FROM otel.otel_traces WHERE TraceId = '$trace_id' AND SpanAttributes['kyurem.task.status'] != ''")"
[ "$status" = "1" ] \
  && pass "the task status is recorded once for the tree" \
  || fail "expected one task status value, got $status"

contexts="$(query "SELECT countDistinct(SpanAttributes['kyurem.context.id']) FROM otel.otel_traces WHERE TraceId = '$trace_id' AND SpanAttributes['kyurem.context.id'] != ''")"
[ "$contexts" = "1" ] \
  && pass "both hops share one context id" \
  || fail "expected one context id across the tree, got $contexts"

shared="$(query "SELECT count() FROM otel.otel_traces WHERE TraceId = '$trace_id' AND SpanAttributes['kyurem.context.id'] != ''")"
[ "$shared" -ge 2 ] \
  && pass "$shared hops carry the shared context id" \
  || fail "expected both hops to carry the context id, got $shared"

echo
echo "Grafana:  http://localhost:3000 (Explore -> ClickHouse -> Traces)"
echo "Prometheus: http://localhost:9090 (kyurem_telemetry_* series)"
echo "ClickHouse: http://localhost:8123 (user 'default', password '$CH_PASSWORD')"
echo
echo "== $PASS assertions passed — M2 demo OK =="
