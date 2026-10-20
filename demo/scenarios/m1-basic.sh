#!/usr/bin/env bash
#
# Kyurem M1 end-to-end demo ("transparent proxy").
#
# Builds the gateway, two demo agents and the demo client, boots them in the
# right order (echo -> gateway -> upper), then asserts the M1 guarantees
# through the plain A2A client:
#
#   1. gateway health: /healthz, /readyz, /version on the admin listener
#   2. discovery: the agent card is served through the gateway with interface
#      URLs rewritten back to the gateway (GW-3)
#   3. plain round trip:  client -> gateway -> echo  (GW-1/GW-2)
#   4. SSE streaming: multiple chunked events relayed live (GW-2)
#   5. alias routing: /a2a/greeter reaches the echo agent (GW-1)
#   6. agent-to-agent chain:  upper -> gateway -> echo  (GW-1, go-upper demo)
#   7. unknown agents fail closed with 404 (GW-1)
#   8. streamed chain: upper runs a task lifecycle through the gateway
#
# Requirements: bash, curl, go. No Docker needed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# Ports, kept in sync with config/kyurem.example.yaml.
A2A_HOST=127.0.0.1:8080
ADMIN_HOST=127.0.0.1:8081
ECHO_PORT=9010
UPPER_PORT=9011

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

echo "== building binaries =="
mkdir -p demo/bin
go build -o demo/bin/kyurem       ./gateway/cmd/kyurem
go build -o demo/bin/go-echo      ./demo/agents/go-echo
go build -o demo/bin/go-upper     ./demo/agents/go-upper
go build -o demo/bin/demo-client  ./demo/scenarios/client

echo "== validating shipped config =="
./demo/bin/kyurem validate --config config/kyurem.example.yaml

echo "== booting: echo -> gateway -> upper =="
./demo/bin/go-echo --port "$ECHO_PORT" & pids+=($!)
./demo/bin/kyurem serve --config config/kyurem.example.yaml >/dev/null 2>&1 & pids+=($!)
./demo/bin/go-upper --port "$UPPER_PORT" \
  --echo-card-url "http://$A2A_HOST/a2a/echo/.well-known/agent-card.json" & pids+=($!)

wait_http "http://$ADMIN_HOST/healthz" "gateway admin"
wait_http "http://$A2A_HOST/a2a/echo/.well-known/agent-card.json" "gateway echo route"
wait_http "http://$A2A_HOST/a2a/upper/.well-known/agent-card.json" "gateway upper route"

echo "-- 1. health endpoints --"
curl -fsS "http://$ADMIN_HOST/healthz" | grep -q '"status":"ok"' \
  && pass "healthz ok" || fail "healthz wrong"
curl -fsS "http://$ADMIN_HOST/readyz" | grep -q '"agents":2' \
  && pass "readyz lists 2 agents" || fail "readyz wrong"
curl -fsS "http://$ADMIN_HOST/version" | grep -q 'a2a_spec_version' \
  && pass "version reports spec version" || fail "version wrong"

echo "-- 2. discovery: card through the gateway is rewritten --"
card="$(curl -fsS "http://$A2A_HOST/a2a/echo/.well-known/agent-card.json")"
echo "$card" | grep -q '"name":"go-echo"' || fail "card name missing"
echo "$card" | grep -qF '"protocolBinding":"JSONRPC"' || fail "card binding missing"
echo "$card" | grep -qF "\"url\":\"http://$A2A_HOST/a2a/echo\"" \
  && pass "card interface URL rewritten to the gateway" || fail "card URL not rewritten: $card"
echo "$card" | grep -qv 'GRPC' \
  && pass "card carries no gRPC interface (GW-8 is out of scope in M1)" || true

echo "-- 3. plain round trip through the gateway --"
out="$(./demo/bin/demo-client --gateway-url "http://$A2A_HOST/a2a/echo" --message "hello gateway")"
echo "$out" | grep -q '^RESULT: echo: hello gateway$' \
  && pass "echo round trip result correct" || fail "unexpected: $out"

echo "-- 4. SSE streaming through the gateway --"
stream_out="$(./demo/bin/demo-client --gateway-url "http://$A2A_HOST/a2a/echo" \
  --message "hello gateway" --stream 5s)"
echo "$stream_out" | grep -q '^chunk:echo: hello$' || fail "first chunk missing: $stream_out"
echo "$stream_out" | grep -q 'TASK_STATE_COMPLETED' \
  || fail "completed status event missing: $stream_out"
echo "$stream_out" | grep -q '^RESULT: echo: hello gateway$' \
  && pass "streamed chunks reassemble to the echo reply" || fail "stream result wrong: $stream_out"

echo "-- 5. alias routing --"
out="$(./demo/bin/demo-client --gateway-url "http://$A2A_HOST/a2a/greeter" --message "alias check")"
echo "$out" | grep -q '^RESULT: echo: alias check$' \
  && pass "alias /a2a/greeter reaches echo" || fail "unexpected: $out"

echo "-- 6. agent-to-agent chain: upper -> gateway -> echo --"
out="$(./demo/bin/demo-client --gateway-url "http://$A2A_HOST/a2a/upper" --message "hello gateway")"
echo "$out" | grep -q '^RESULT: upper: HELLO GATEWAY$' \
  && pass "chained round trip (marker proves it went through echo)" || fail "unexpected: $out"

echo "-- 7. unknown agent fails closed --"
code="$(curl -s -o /dev/null -w '%{http_code}' "http://$A2A_HOST/a2a/does-not-exist")"
[ "$code" = "404" ] && pass "unknown agent -> 404" || fail "unknown agent -> HTTP $code"

echo "-- 8. streamed chain: upper runs a task lifecycle through the gateway --"
chain_stream="$(./demo/bin/demo-client --gateway-url "http://$A2A_HOST/a2a/upper" \
  --message "hello gateway" --stream 5s)"
echo "$chain_stream" | grep -q 'TASK_STATE_COMPLETED' \
  || fail "upper stream has no completed state: $chain_stream"
echo "$chain_stream" | grep -q '^RESULT: upper: HELLO GATEWAY$' \
  && pass "streamed chain completes with the upper reply" || fail "unexpected: $chain_stream"

echo
echo "== $PASS assertions passed — M1 demo OK =="
