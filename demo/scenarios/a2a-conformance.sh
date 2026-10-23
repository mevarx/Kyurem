#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TCK_ROOT="${A2A_TCK_DIR:?set A2A_TCK_DIR to the checked-out a2a-tck directory}"
ITK_ROOT="${A2A_ITK_DIR:?set A2A_ITK_DIR to the checked-out a2a-itk directory}"
REPORT_ROOT="${A2A_REPORT_DIR:-$ROOT/demo/bin/conformance-results}"
TMP="$(mktemp -d)"
PIDS=()
cleanup() {
  for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  for pid in "${PIDS[@]:-}"; do wait "$pid" 2>/dev/null || true; done
  rm -rf "$TMP"
}
trap cleanup EXIT

mkdir -p "$REPORT_ROOT/itk"
rm -rf "$TCK_ROOT/reports"
"$ROOT/demo/agents/python/.venv/bin/python" -m kyurem_demo echo --port 9012 >"$TMP/agent.log" 2>&1 & PIDS+=("$!")
go build -o "$TMP/kyurem" "$ROOT/gateway/cmd/kyurem"
"$TMP/kyurem" serve --config "$ROOT/config/kyurem.conformance.yaml" >"$TMP/gateway.log" 2>&1 & PIDS+=("$!")
wait_http() {
  for _ in $(seq 1 80); do
    if curl -fsS -o /dev/null "$1" 2>/dev/null; then return; fi
    sleep 0.25
  done
  echo "service did not become ready: $1" >&2
  exit 1
}
wait_http http://127.0.0.1:9012/.well-known/agent-card.json
wait_http http://127.0.0.1:8081/healthz

TCK_STATUS=0
if (
  cd "$TCK_ROOT" && uv run --project "$TCK_ROOT" --frozen python run_tck.py \
    --sut-host http://127.0.0.1:8080/a2a/python-echo \
    --transport jsonrpc --level must
); then
  :
else
  TCK_STATUS=$?
  echo "A2A TCK runner exited with status $TCK_STATUS" >&2
fi
if ! python3 "$ROOT/demo/scenarios/check-a2a-tck-report.py" "$TCK_ROOT/reports/compatibility.json"; then
  TCK_STATUS=1
fi

ITK_STATUS=0
if (
  cd "$ITK_ROOT" && uv run --project "$ITK_ROOT" --frozen python run_acts.py \
    --sut-url http://127.0.0.1:9012 --sdk kyurem-python-demo \
    --transport jsonrpc \
    --behaviors "$ROOT/demo/agents/python/acts/sut-behaviors.yaml" \
    --test CORE-SEND-001 --test CORE-GET-001 \
    --test CARD-DISC-001 --test CARD-DISC-002 \
    --out "$REPORT_ROOT/itk"
); then
  :
else
  ITK_STATUS=$?
  echo "A2A ITK runner exited with status $ITK_STATUS" >&2
fi

if [[ $TCK_STATUS -ne 0 || $ITK_STATUS -ne 0 ]]; then
  exit 1
fi
