#!/usr/bin/env bash
set -euo pipefail

# Runs the A/B/C robustness matrix with controlled one-way delay injected before
# every outgoing peer Replicate RPC. No existing output directory is reused.

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

mkdir -p "$OUT_DIR" "$OUT_DIR/bin" "$OUT_DIR/logs" "$OUT_DIR/checker"
cd "$PROJECT_ROOT"

PORTS=(50051 50052 50053)
PEERS=(
  "localhost:50052,localhost:50053"
  "localhost:50051,localhost:50053"
  "localhost:50051,localhost:50052"
)
MODES=(eventual causal strong)
DELAYS_MS=(0 5 20)
WORKLOADS=(workload_a workload_b workload_c)
SEEDS=(1 2 3 4 5)
NODE_PIDS=()

source_manifest() {
  find . -type f \
    \( -name '*.go' -o -name '*.proto' -o -name '*.yaml' -o -name '*.yml' \
       -o -name '*.mod' -o -name '*.sum' -o -name '*.sh' -o -name 'Dockerfile' \
       -o -name 'README.md' \) \
    -not -path './.git/*' \
    -not -path './bin/*' \
    -not -path './bench/results/*' \
    -not -path './docs/codebase/*' \
    -print0 | sort -z | xargs -0 sha256sum
}

stop_cluster() {
  local pid
  for pid in "${NODE_PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
  done
  for pid in "${NODE_PIDS[@]:-}"; do
    wait "$pid" 2>/dev/null || true
  done
  NODE_PIDS=()
}

trap stop_cluster EXIT INT TERM

assert_ports_free() {
  local port
  for port in "${PORTS[@]}"; do
    if ss -H -ltn | awk '{print $4}' | grep -Eq ":${port}$"; then
      echo "port ${port} is already in use; aborting without killing it" >&2
      exit 2
    fi
  done
}

wait_for_cluster() {
  local port attempt ready
  ready=0
  for port in "${PORTS[@]}"; do
    for attempt in $(seq 1 100); do
      if "$OUT_DIR/bin/wait-grpc" "localhost:${port}" >/dev/null 2>&1; then
        ready=$((ready + 1))
        break
      fi
      sleep 0.1
    done
  done
  if [[ "$ready" -ne 3 ]]; then
    echo "cluster failed readiness check (${ready}/3 nodes)" >&2
    exit 1
  fi
}

start_cluster() {
  local mode="$1"
  local delay_ms="$2"
  local i
  assert_ports_free
  NODE_PIDS=()
  for i in 0 1 2; do
    "$OUT_DIR/bin/node" \
      --mode="$mode" \
      --port="${PORTS[$i]}" \
      --peers="${PEERS[$i]}" \
      --peer-delay="${delay_ms}ms" \
      --replicate-delay-prefix=chain-a- \
      --replicate-delay=150ms \
      >"$OUT_DIR/logs/delay${delay_ms}_${mode}_node$((i + 1)).log" 2>&1 &
    NODE_PIDS+=("$!")
  done
  wait_for_cluster
}

{
  echo "evaluation_started=$(date --iso-8601=seconds)"
  echo "project_root=$PROJECT_ROOT"
  echo "git_commit=$(git rev-parse HEAD 2>/dev/null || echo unavailable)"
  echo "go_version=$(go version)"
  echo "kernel=$(uname -a)"
  echo "delay_semantics=one-way pre-Replicate-RPC sleep on each outgoing peer call"
  echo "cpu_governor=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null || echo unavailable)"
  echo
  lscpu
  echo
  free -h
  echo
  uptime
} >"$OUT_DIR/environment.txt"
git status --short >"$OUT_DIR/git_status.before.txt" || true
source_manifest >"$OUT_DIR/source_manifest.before.sha256"

echo "[delay] building isolated binaries"
go build -o "$OUT_DIR/bin/node" ./cmd/node
go build -o "$OUT_DIR/bin/bench" ./cmd/bench
go build -o "$OUT_DIR/bin/checker" ./cmd/checker
go build -o "$OUT_DIR/bin/experiments" ./cmd/experiments
go build -o "$OUT_DIR/bin/wait-grpc" ./scripts/wait_grpc.go

printf '%s\n' 'mode,peer_delay_ms,workload,seed,skip_load' >"$OUT_DIR/run_manifest.csv"
printf '%s\n' 'mode,peer_delay_ms,workload,seed,pass,elapsed_ms,keys_checked,settle_limit_ms' \
  >"$OUT_DIR/convergence_delay.csv"

for delay_ms in "${DELAYS_MS[@]}"; do
  for mode in "${MODES[@]}"; do
    echo "[delay] starting mode=${mode} injected_delay=${delay_ms}ms"
    start_cluster "$mode" "$delay_ms"
    first_run=1

    for workload in "${WORKLOADS[@]}"; do
      for seed in "${SEEDS[@]}"; do
        skip_args=()
        skip_label=0
        if [[ "$first_run" -eq 0 ]]; then
          skip_args+=(--skip-load)
          skip_label=1
        fi
        echo "[delay] benchmark mode=${mode} delay=${delay_ms} workload=${workload} seed=${seed} skip_load=${skip_label}"
        "$OUT_DIR/bin/bench" \
          --target=localhost:50051 \
          --workload="bench/workloads/${workload}.yaml" \
          --mode="$mode" \
          --seed="$seed" \
          --peer-delay-ms="$delay_ms" \
          --output="$OUT_DIR/benchmark_delay.csv" \
          --timeout=10m \
          "${skip_args[@]}" \
          2>&1 | tee "$OUT_DIR/logs/bench_delay${delay_ms}_${mode}_${workload}_seed${seed}.log"
        printf '%s,%s,%s,%s,%s\n' \
          "$mode" "$delay_ms" "$workload" "$seed" "$skip_label" \
          >>"$OUT_DIR/run_manifest.csv"
        first_run=0

        checker_log="$OUT_DIR/checker/delay${delay_ms}_${mode}_${workload}_seed${seed}.log"
        start_ns="$(date +%s%N)"
        set +e
        "$OUT_DIR/bin/checker" \
          --nodes=localhost:50051,localhost:50052,localhost:50053 \
          --key-prefix=user \
          --key-count=1000 \
          --settle=10s \
          --timeout=15s \
          >"$checker_log" 2>&1
        checker_rc=$?
        set -e
        end_ns="$(date +%s%N)"
        elapsed_ms=$(((end_ns - start_ns) / 1000000))
        if [[ "$checker_rc" -eq 0 ]]; then
          checker_pass=1
        else
          checker_pass=0
        fi
        printf '%s,%s,%s,%s,%s,%s,1000,10000\n' \
          "$mode" "$delay_ms" "$workload" "$seed" "$checker_pass" "$elapsed_ms" \
          >>"$OUT_DIR/convergence_delay.csv"
      done
    done

    correctness_file="$OUT_DIR/correctness_delay${delay_ms}.csv"
    "$OUT_DIR/bin/experiments" \
      --test=staleness \
      --origin=localhost:50051 \
      --replica=localhost:50052 \
      --mode="$mode" \
      --trials=50 \
      --output="$correctness_file" \
      2>&1 | tee "$OUT_DIR/logs/staleness_delay${delay_ms}_${mode}.log"
    "$OUT_DIR/bin/experiments" \
      --test=causal \
      --origin=localhost:50051 \
      --replica=localhost:50052 \
      --mode="$mode" \
      --trials=30 \
      --output="$correctness_file" \
      2>&1 | tee "$OUT_DIR/logs/causal_delay${delay_ms}_${mode}.log"

    stop_cluster
    sleep 0.25
  done
done

git status --short >"$OUT_DIR/git_status.after.txt" || true
source_manifest >"$OUT_DIR/source_manifest.after.sha256"
if cmp -s "$OUT_DIR/source_manifest.before.sha256" "$OUT_DIR/source_manifest.after.sha256"; then
  echo "unchanged" >"$OUT_DIR/source_integrity.txt"
else
  echo "CHANGED" >"$OUT_DIR/source_integrity.txt"
  diff -u "$OUT_DIR/source_manifest.before.sha256" "$OUT_DIR/source_manifest.after.sha256" \
    >"$OUT_DIR/source_manifest.diff" || true
  echo "source/configuration integrity check failed" >&2
  exit 1
fi

echo "evaluation_finished=$(date --iso-8601=seconds)" >>"$OUT_DIR/environment.txt"
find "$OUT_DIR" -type f ! -name RAW_SHA256SUMS.txt -print0 \
  | sort -z | xargs -0 sha256sum >"$OUT_DIR/RAW_SHA256SUMS.txt"
chmod -R a-w "$OUT_DIR"

echo "[delay] completed: $OUT_DIR"
