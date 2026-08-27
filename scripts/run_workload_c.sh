#!/usr/bin/env bash
# Measure workload C (100% read) on a live 3-node cluster, 3 seeds per mode.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PORTS=(50051 50052 50053)
PEERS=(
  "localhost:50052,localhost:50053"
  "localhost:50051,localhost:50053"
  "localhost:50051,localhost:50052"
)
MODES=(eventual causal strong)
SEEDS=(1 2 3)
CSV="${OUT:-bench/results/paper/summary_workload_c.csv}"
PIDS=()

cleanup() {
  for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT

kill_ports() {
  for p in "${PORTS[@]}"; do
    fuser -k "${p}/tcp" 2>/dev/null || true
  done
  sleep 0.8
}

start_cluster() {
  local mode="$1"
  kill_ports
  PIDS=()
  for i in 0 1 2; do
    bin/node --mode="$mode" --port="${PORTS[$i]}" --peers="${PEERS[$i]}" \
      >"/tmp/kvstore-c-node$((i+1))-${mode}.log" 2>&1 &
    PIDS+=("$!")
  done
  local ready=0
  for p in "${PORTS[@]}"; do
    for _ in $(seq 1 40); do
      if go run ./scripts/wait_grpc.go "localhost:${p}" >/dev/null 2>&1; then
        ready=$((ready+1))
        break
      fi
      sleep 0.2
    done
  done
  [[ "$ready" -eq 3 ]]
}

rm -f "$CSV"
mkdir -p "$(dirname "$CSV")"

for mode in "${MODES[@]}"; do
  echo "==> starting cluster mode=${mode}"
  if ! start_cluster "$mode"; then
    echo "FAIL: cluster ${mode}" >&2
    exit 1
  fi
  for seed in "${SEEDS[@]}"; do
    echo "---- ${mode} / workload_c / seed=${seed}"
    bin/bench --target=localhost:50051 \
      --workload=bench/workloads/workload_c.yaml \
      --mode="$mode" --output="$CSV" --seed="$seed"
  done
  bin/checker --nodes=localhost:50051,localhost:50052,localhost:50053 \
    --key-prefix=user --key-count=100 --settle=3s || true
done

echo "==> CSV: ${CSV}"
cat "$CSV"
