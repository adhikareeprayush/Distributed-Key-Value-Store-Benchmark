#!/usr/bin/env bash
set -euo pipefail

# Confirmation audit for delay-matrix checker timeouts. The original failures
# remain immutable; these fresh reruns are stored separately and are not used in
# performance aggregates.

if [[ "$#" -ne 2 ]]; then
  echo "usage: $0 PROJECT_ROOT OUTPUT_DIR" >&2
  exit 2
fi

PROJECT_ROOT="$(realpath "$1")"
OUT_DIR="$(realpath -m "$2")"
if [[ -e "$OUT_DIR" ]]; then
  echo "refusing to reuse existing output directory: $OUT_DIR" >&2
  exit 2
fi

mkdir -p "$OUT_DIR/bin" "$OUT_DIR/logs"
cd "$PROJECT_ROOT"

PORTS=(50051 50052 50053)
PEERS=(
  "localhost:50052,localhost:50053"
  "localhost:50051,localhost:50053"
  "localhost:50051,localhost:50052"
)
NODE_PIDS=()

source_manifest() {
  find . -type f \
    \( -name '*.go' -o -name '*.proto' -o -name '*.yaml' -o -name '*.yml' \
       -o -name '*.mod' -o -name '*.sum' -o -name '*.sh' -o -name 'Dockerfile' \
       -o -name 'README.md' \) \
    -not -path './.git/*' -not -path './bin/*' -not -path './bench/results/*' \
    -not -path './docs/codebase/*' -print0 | sort -z | xargs -0 sha256sum
}

stop_cluster() {
  local pid
  for pid in "${NODE_PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  for pid in "${NODE_PIDS[@]:-}"; do wait "$pid" 2>/dev/null || true; done
  NODE_PIDS=()
}
trap stop_cluster EXIT INT TERM

wait_for_cluster() {
  local port attempt ready=0
  for port in "${PORTS[@]}"; do
    for attempt in $(seq 1 100); do
      if "$OUT_DIR/bin/wait-grpc" "localhost:${port}" >/dev/null 2>&1; then
        ready=$((ready + 1))
        break
      fi
      sleep 0.1
    done
  done
  [[ "$ready" -eq 3 ]]
}

start_cluster() {
  local mode="$1" delay_ms="$2" i
  for i in 0 1 2; do
    "$OUT_DIR/bin/node" --mode="$mode" --port="${PORTS[$i]}" \
      --peers="${PEERS[$i]}" --peer-delay="${delay_ms}ms" \
      >"$OUT_DIR/logs/${mode}_delay${delay_ms}_node$((i + 1)).log" 2>&1 &
    NODE_PIDS+=("$!")
  done
  wait_for_cluster
}

source_manifest >"$OUT_DIR/source_manifest.before.sha256"
git status --short >"$OUT_DIR/git_status.before.txt" || true
go build -o "$OUT_DIR/bin/node" ./cmd/node
go build -o "$OUT_DIR/bin/bench" ./cmd/bench
go build -o "$OUT_DIR/bin/checker" ./cmd/checker
go build -o "$OUT_DIR/bin/wait-grpc" ./scripts/wait_grpc.go

printf '%s\n' 'mode,peer_delay_ms,workload,seed,phase,pass,elapsed_ms,keys_checked,settle_limit_ms,overall_timeout_ms' \
  >"$OUT_DIR/confirmation.csv"

# Exact cells that timed out in the immutable primary delay matrix.
CASES=(
  'eventual 5 workload_c 4'
  'strong 5 workload_c 3'
  'causal 20 workload_a 3'
)

for case_spec in "${CASES[@]}"; do
  read -r mode delay_ms workload seed <<<"$case_spec"
  echo "[confirm] mode=${mode} delay=${delay_ms} workload=${workload} seed=${seed}"
  start_cluster "$mode" "$delay_ms"
  "$OUT_DIR/bin/bench" --target=localhost:50051 \
    --workload="bench/workloads/${workload}.yaml" --mode="$mode" --seed="$seed" \
    --peer-delay-ms="$delay_ms" --output="$OUT_DIR/confirmation_benchmark.csv" \
    --timeout=10m >"$OUT_DIR/logs/bench_${mode}_delay${delay_ms}_${workload}_seed${seed}.log" 2>&1

  for phase in immediate after_5s; do
    if [[ "$phase" == after_5s ]]; then sleep 5; fi
    log_file="$OUT_DIR/logs/check_${mode}_delay${delay_ms}_${workload}_seed${seed}_${phase}.log"
    start_ns="$(date +%s%N)"
    set +e
    "$OUT_DIR/bin/checker" --nodes=localhost:50051,localhost:50052,localhost:50053 \
      --key-prefix=user --key-count=1000 --settle=10s --timeout=60s \
      >"$log_file" 2>&1
    checker_rc=$?
    set -e
    end_ns="$(date +%s%N)"
    elapsed_ms=$(((end_ns - start_ns) / 1000000))
    if [[ "$checker_rc" -eq 0 ]]; then pass=1; else pass=0; fi
    printf '%s,%s,%s,%s,%s,%s,%s,1000,10000,60000\n' \
      "$mode" "$delay_ms" "$workload" "$seed" "$phase" "$pass" "$elapsed_ms" \
      >>"$OUT_DIR/confirmation.csv"
  done
  stop_cluster
  sleep 0.25
done

git status --short >"$OUT_DIR/git_status.after.txt" || true
source_manifest >"$OUT_DIR/source_manifest.after.sha256"
if cmp -s "$OUT_DIR/source_manifest.before.sha256" "$OUT_DIR/source_manifest.after.sha256"; then
  echo unchanged >"$OUT_DIR/source_integrity.txt"
else
  echo CHANGED >"$OUT_DIR/source_integrity.txt"
  exit 1
fi
find "$OUT_DIR" -type f ! -name RAW_SHA256SUMS.txt -print0 \
  | sort -z | xargs -0 sha256sum >"$OUT_DIR/RAW_SHA256SUMS.txt"
chmod -R a-w "$OUT_DIR"
echo "[confirm] completed: $OUT_DIR"
