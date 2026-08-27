#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PORTS=(50051 50052 50053)
PEERS=(
  "localhost:50052,localhost:50053"
  "localhost:50051,localhost:50053"
  "localhost:50051,localhost:50052"
)
PIDS=()
PASS=0
FAIL=0

log() { printf '[e2e] %s\n' "$*"; }
pass() { PASS=$((PASS + 1)); log "PASS: $*"; }
fail() { FAIL=$((FAIL + 1)); log "FAIL: $*"; }

cleanup() {
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT

kill_ports() {
  for port in "${PORTS[@]}"; do
    fuser -k "${port}/tcp" 2>/dev/null || true
  done
  sleep 0.5
}

build() {
  log "building binaries..."
  go build -o bin/node ./cmd/node
  go build -o bin/bench ./cmd/bench
  go build -o bin/checker ./cmd/checker
}

start_cluster() {
  local mode="$1"
  kill_ports
  PIDS=()
  for i in 0 1 2; do
    bin/node --mode="$mode" --port="${PORTS[$i]}" --peers="${PEERS[$i]}" \
      >"/tmp/kvstore-node$((i+1)).log" 2>&1 &
    PIDS+=("$!")
  done
  sleep 1
}

wait_for_nodes() {
  local ready=0
  for port in "${PORTS[@]}"; do
    for _ in $(seq 1 50); do
      if go run ./scripts/wait_grpc.go "localhost:${port}" >/dev/null 2>&1; then
        ready=$((ready + 1))
        break
      fi
      sleep 0.2
    done
  done
  if [[ "$ready" -ne 3 ]]; then
    fail "cluster did not become ready ($ready/3 ports open)"
    return 1
  fi
  pass "cluster ready on ports ${PORTS[*]}"
}

test_basic_rpc() {
  if go run ./scripts/test_client.go; then
    pass "basic Put/Get/Delete RPC"
  else
    fail "basic Put/Get/Delete RPC"
    return 1
  fi
}

test_bench_and_checker() {
  local mode="$1"
  local out="/tmp/kvstore-bench-${mode}.csv"
  if bin/bench \
    --target=localhost:50051 \
    --workload=bench/workloads/smoke.yaml \
    --mode="$mode" \
    --output="$out" \
    --seed=1; then
    pass "benchmark ($mode)"
  else
    fail "benchmark ($mode)"
    return 1
  fi

  if bin/checker \
    --nodes=localhost:50051,localhost:50052,localhost:50053 \
    --key-prefix=user \
    --key-count=20 \
    --settle=3s; then
    pass "checker convergence ($mode)"
  else
    fail "checker convergence ($mode)"
    return 1
  fi
}

test_mode() {
  local mode="$1"
  log "=== testing mode: $mode ==="
  start_cluster "$mode"
  wait_for_nodes
  test_basic_rpc
  test_bench_and_checker "$mode"
}

main() {
  build
  for mode in eventual causal strong; do
    test_mode "$mode" || true
  done

  log "=============================="
  log "results: $PASS passed, $FAIL failed"
  if [[ "$FAIL" -gt 0 ]]; then
    exit 1
  fi
}

main "$@"
