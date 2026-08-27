#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TARGET="${TARGET:-localhost:50051}"
MODE="${MODE:-eventual}"
WORKLOAD="${WORKLOAD:-bench/workloads/workload_a.yaml}"
OUTPUT="${OUTPUT:-bench/results/summary.csv}"
SEED="${SEED:-42}"

echo "==> Building binaries"
go build -o bin/node ./cmd/node
go build -o bin/bench ./cmd/bench
go build -o bin/checker ./cmd/checker

echo "==> Running workload against ${TARGET} (mode label=${MODE})"
bin/bench \
  --target="${TARGET}" \
  --workload="${WORKLOAD}" \
  --mode="${MODE}" \
  --output="${OUTPUT}" \
  --seed="${SEED}"

echo "==> Checking replica convergence"
bin/checker \
  --nodes="${NODES:-localhost:50051,localhost:50052,localhost:50053}" \
  --key-prefix=user \
  --key-count=100 \
  --settle=3s

echo "==> Done. Results: ${OUTPUT}"
