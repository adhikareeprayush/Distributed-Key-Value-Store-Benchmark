#!/usr/bin/env bash
# Run the experiment matrix from docs/experiment_plan.md (Section 6).
# Restart the cluster with the target MODE before each mode block:
#   docker compose down && MODE=causal docker compose up --build -d
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MODE="${MODE:-eventual}"
TARGET="${TARGET:-localhost:50051}"
NODES="${NODES:-localhost:50051,localhost:50052,localhost:50053}"
OUT_DIR="${OUT_DIR:-bench/results/paper}"
SEEDS="${SEEDS:-1 2 3 4 5}"

WORKLOADS=(
  bench/workloads/workload_a.yaml
  bench/workloads/workload_b.yaml
  bench/workloads/workload_c.yaml
  bench/workloads/workload_write_heavy.yaml
  bench/workloads/workload_uniform_a.yaml
)

mkdir -p "$OUT_DIR"
CSV="${OUT_DIR}/summary_${MODE}.csv"

echo "==> Building binaries"
go build -o bin/bench ./cmd/bench
go build -o bin/checker ./cmd/checker

echo "==> Mode=${MODE}  CSV=${CSV}"

for workload in "${WORKLOADS[@]}"; do
  for seed in $SEEDS; do
    name="$(basename "$workload" .yaml)"
    echo "---- ${MODE} / ${name} / seed=${seed}"
    bin/bench \
      --target="${TARGET}" \
      --workload="${workload}" \
      --mode="${MODE}" \
      --output="${CSV}" \
      --seed="${seed}"
  done
done

echo "==> Convergence check"
bin/checker \
  --nodes="${NODES}" \
  --key-prefix=user \
  --key-count=200 \
  --settle=5s

echo "==> Finished. Results: ${CSV}"
