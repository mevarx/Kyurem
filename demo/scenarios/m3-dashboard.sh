#!/usr/bin/env bash
#
# Kyurem M3 end-to-end demo ("a scripted failure is diagnosable").
#
# Boots the gateway with the in-process span store, sends one failing task
# through it and reads the result back through the management API, which is
# exactly what the dashboard does:
#
#   1. the failing task appears in /api/v1/tasks with status "failed"
#   2. /api/v1/tasks/{id} names the failing span and the error type
#   3. the root-cause panel has a (clearly labelled) heuristic hint
#   4. the overview counts the failure
#
# Requirements: bash, curl, go. No Docker, no ClickHouse, no dashboard.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

A2A_HOST=127.0.0.1:8080
ADMIN_HOST=127.0.0.1:8081
MGMT_HOST=127.0.0.1:8082

PASS=0
pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do
    kill "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { PASS=$((PASS + 1)); echo "PASS: $*"; }

wait_http() { # url, name
  local url="$1" name="$2"
  for _ in $(seq 1 50); do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then
      return 0
    fi
    sleep 0.2
  done
  fail "$name not ready: $url"
}

# api_get <path> -> JSON body
api_get() {
  curl -fsS "http://$MGMT_HOST$1"
}

# json_value <json> <key> -> the raw value of a top-level key
json_value() {
  printf '%s' "$1" | sed -n "s/.*\"$2\":\"\([^\"]*\)\".*/\1/p" | head -n 1
}

echo "== building binaries =="
mkdir -p demo/bin
go build -o demo/bin/kyurem ./gateway/cmd/kyurem

echo "== validating the shipped config =="
./demo/bin/kyurem validate --config config/kyurem.example.yaml >/dev/null

echo "== booting the gateway (no upstream agents: every call fails) =="
./demo/bin/kyurem serve --config config/kyurem.example.yaml >/dev/null 2>&1 & pids+=($!)

wait_http "http://$ADMIN_HOST/healthz" "gateway admin"
wait_http "http://$MGMT_HOST/healthz" "gateway management API"

echo "-- 1. a failing task reaches the management API --"
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://$A2A_HOST/a2a/echo" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":"m3-1","method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hello"}]}}}')"
[ "$code" = "502" ] && pass "the unreachable upstream answers 502" || fail "expected 502, got $code"

tasks=""
for _ in $(seq 1 50); do
  tasks="$(api_get '/api/v1/tasks?limit=50')"
  echo "$tasks" | grep -q '"items":\[{' && break
  sleep 0.2
done
echo "$tasks" | grep -q '"items":\[{' || fail "the failed task did not reach the management API"

status="$(json_value "$tasks" status)"
[ "$status" = "failed" ] && pass "the task list shows the failed task" || fail "task status = $status"

error_type="$(printf '%s' "$tasks" | sed -n 's/.*"error_type":"\([^"]*\)".*/\1/p' | head -n 1)"
[ -n "$error_type" ] && pass "the task names the failure: $error_type" || fail "no error type on the failed task"

echo "-- 2. the task detail carries the span tree and the root cause --"
task_id="$(json_value "$tasks" task_id)"
detail="$(api_get "/api/v1/tasks/$task_id")"
echo "$detail" | grep -q '"spans":\[{' || fail "the detail has no spans"
pass "the detail lists the span tree"

echo "$detail" | grep -q '"root_cause":{' || fail "the detail has no root-cause hint"
echo "$detail" | grep -q '"confidence":"heuristic"' || fail "the root cause is not labelled as a heuristic"
message="$(printf '%s' "$detail" | sed -n 's/.*"message":"\([^"]*\)".*/\1/p' | head -n 1)"
[ -n "$message" ] && pass "root-cause hint: $message" || fail "the root cause has no message"

echo "-- 3. the overview counts the failure --"
overview="$(api_get '/api/v1/overview')"
failed="$(printf '%s' "$overview" | sed -n 's/.*"tasks_failed":\([0-9]*\).*/\1/p' | head -n 1)"
[ "${failed:-0}" -ge 1 ] && pass "overview: $failed failed task(s)" || fail "overview tasks_failed = ${failed:-0}"

echo "-- 4. unknown cost stays unknown --"
costs="$(api_get '/api/v1/costs?group_by=agent')"
echo "$costs" | grep -q '"total_usd":null' || fail "the cost must be unknown (null), got: $costs"
pass "the cost is unknown, not zero"

echo
echo "== $PASS assertions passed — M3 demo OK =="
echo "The dashboard shows the same data: 'make dashboard-build' and open the task list."
