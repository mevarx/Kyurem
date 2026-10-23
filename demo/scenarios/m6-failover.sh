#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

PY_PROJECT="$ROOT/demo/agents/python"
A2A_PORT="${KYUREM_DEMO_A2A_PORT:-18080}"
ADMIN_PORT="${KYUREM_DEMO_ADMIN_PORT:-18081}"
MGMT_PORT="${KYUREM_DEMO_MGMT_PORT:-18082}"
ECHO_PORT="${KYUREM_DEMO_ECHO_PORT:-19012}"
FLAKY_PORT="${KYUREM_DEMO_FLAKY_PORT:-19014}"
DASH_PORT="${KYUREM_DEMO_DASH_PORT:-5173}"
TMP="$(mktemp -d)"
PIDS=()
cleanup() {
  for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  for pid in "${PIDS[@]:-}"; do wait "$pid" 2>/dev/null || true; done
  rm -rf "$TMP"
}
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

cat >"$TMP/config.yaml" <<EOF_CONFIG
listeners:
  a2a:
    addr: "127.0.0.1:${A2A_PORT}"
    public_base_url: "http://127.0.0.1:${A2A_PORT}"
  admin:
    addr: "127.0.0.1:${ADMIN_PORT}"
  mgmt:
    addr: "127.0.0.1:${MGMT_PORT}"
management:
  enabled: true
  storage:
    driver: memory
    memory_capacity: 4096
failure_handling:
  max_attempts: 2
  retry_budget:
    max_retries: 10
    window: 1m
  circuit_breaker:
    failure_threshold: 5
    open_duration: 30s
  max_retry_body_bytes: 65536
agents:
  - name: failover-demo
    tags: [demo, failover]
    upstreams:
      - url: "http://127.0.0.1:${FLAKY_PORT}"
        binding: JSONRPC
      - url: "http://127.0.0.1:${ECHO_PORT}"
        binding: JSONRPC
EOF_CONFIG

uv sync --project "$PY_PROJECT" --locked --quiet
"$PY_PROJECT/.venv/bin/python" -m kyurem_demo echo --port "$ECHO_PORT" >"$TMP/echo.log" 2>&1 & PIDS+=("$!")
"$PY_PROJECT/.venv/bin/python" -m kyurem_demo flaky --port "$FLAKY_PORT" --failures 1 >"$TMP/flaky.log" 2>&1 & PIDS+=("$!")
go build -o "$TMP/kyurem" ./gateway/cmd/kyurem
"$TMP/kyurem" validate --config "$TMP/config.yaml" >/dev/null
"$TMP/kyurem" serve --config "$TMP/config.yaml" >"$TMP/gateway.log" 2>&1 & PIDS+=("$!")

wait_http() {
  local url="$1" label="$2"
  for _ in $(seq 1 80); do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then return; fi
    sleep 0.25
  done
  fail "$label failed to start ($url); inspect $TMP"
}
wait_http "http://127.0.0.1:${ECHO_PORT}/.well-known/agent-card.json" "Python echo"
wait_http "http://127.0.0.1:${FLAKY_PORT}/.well-known/agent-card.json" "Python flaky agent"
wait_http "http://127.0.0.1:${ADMIN_PORT}/healthz" "gateway"
wait_http "http://127.0.0.1:${MGMT_PORT}/healthz" "management API"

if [ -d dashboard/node_modules ]; then
  (cd dashboard && exec env KYUREM_API_ORIGIN="http://127.0.0.1:${MGMT_PORT}" node node_modules/vite/bin/vite.js --host 127.0.0.1 --port "$DASH_PORT") >"$TMP/dashboard.log" 2>&1 & PIDS+=("$!")
  wait_http "http://127.0.0.1:${DASH_PORT}/" "dashboard"
fi

TASK_KEY="m6-failover-$(date +%s)-$$"
status="$(curl -sS -o "$TMP/response.json" -w '%{http_code}' -X POST "http://127.0.0.1:${A2A_PORT}/a2a/failover-demo" \
  -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' -H "Idempotency-Key: m6-demo-${TASK_KEY}" \
  -d "$(python3 -c 'import json,sys; k=sys.argv[1]; print(json.dumps({"jsonrpc":"2.0","id":k,"method":"SendMessage","params":{"message":{"messageId":k,"role":"ROLE_USER","parts":[{"text":"show retry and failover"}]}}}))' "$TASK_KEY")")"
[ "$status" = 200 ] || fail "gateway returned HTTP $status: $(cat "$TMP/response.json")"

TASK_ID=""
DETAIL=""
for _ in $(seq 1 80); do
  TASKS="$(curl -fsS "http://127.0.0.1:${MGMT_PORT}/api/v1/tasks?limit=50")"
  TASK_ID="$(python3 -c 'import json,sys; d=json.load(sys.stdin); print(next((i.get("task_id", "") for i in d.get("items", []) if i.get("task_id")), ""))' <<<"$TASKS")"
  if [ -n "$TASK_ID" ]; then
    DETAIL="$(curl -fsS "http://127.0.0.1:${MGMT_PORT}/api/v1/tasks/${TASK_ID}" || true)"
    if grep -q 'kyurem.failover' <<<"$DETAIL"; then break; fi
  fi
  sleep 0.25
done
[ -n "$TASK_ID" ] || fail "task was not recorded in the management API"
python3 - "$TMP/response.json" "$DETAIL" "$TASK_ID" <<'PY'
import json, sys
response = json.load(open(sys.argv[1]))
detail = json.loads(sys.argv[2])
text = json.dumps(response)
if "python echo: show retry and failover" not in text:
    raise SystemExit(f"fallback response does not contain the healthy echo result: {text[:1600]}")
if detail.get("task", {}).get("status") not in {"completed", "succeeded"}:
    raise SystemExit(f"dashboard task did not succeed: {detail.get('task', {}).get('status')!r}")
events = detail.get("events", [])
names = {event.get("name") for event in events}
if not {"kyurem.retry", "kyurem.failover"} <= names:
    raise SystemExit(f"expected retry/failover events; got {sorted(names)}")
print(f"PASS: fallback completed task {sys.argv[3]}")
print("PASS: task detail contains kyurem.retry and kyurem.failover span events")
PY

printf '\nFailover task: %s\n' "$TASK_ID"
if [ -d dashboard/node_modules ]; then
  echo "Dashboard: http://127.0.0.1:${DASH_PORT}/tasks/${TASK_ID}"
  if [ "${KYUREM_DEMO_KEEP_RUNNING:-0}" = 1 ]; then
    echo "Press Ctrl+C to stop the gateway, agents, and dashboard."
    wait
  fi
else
  echo "Dashboard API detail: http://127.0.0.1:${MGMT_PORT}/api/v1/tasks/${TASK_ID}"
  echo "Install dashboard dependencies with 'make dashboard-install' to open the UI during this demo."
fi
