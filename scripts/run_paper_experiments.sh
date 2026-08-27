#!/usr/bin/env bash
# Full paper experiment pipeline: build, test, 3-mode benchmarks, correctness experiments.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OUT_DIR="${OUT_DIR:-bench/results/paper}"
REPORT="${OUT_DIR}/REPORT.md"
PORTS=(50051 50052 50053)
PEERS=(
  "localhost:50052,localhost:50053"
  "localhost:50051,localhost:50053"
  "localhost:50051,localhost:50052"
)
MODES=(eventual causal strong)
SEEDS=(1 2 3 4 5)
WORKLOADS=(workload_a workload_b workload_c workload_write_heavy workload_uniform_a)
PIDS=()
PASS=0
FAIL=0

log() { printf '[paper] %s\n' "$*"; }
pass() { PASS=$((PASS+1)); log "PASS: $*"; }
fail() { FAIL=$((FAIL+1)); log "FAIL: $*"; }

cleanup() {
  for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$OUT_DIR"
: > "$REPORT"

record() { echo "$1" | tee -a "$REPORT"; }

kill_ports() {
  for p in "${PORTS[@]}"; do fuser -k "${p}/tcp" 2>/dev/null || true; done
  sleep 0.5
}

start_cluster() {
  local mode="$1"
  kill_ports
  PIDS=()
  for i in 0 1 2; do
    bin/node --mode="$mode" --port="${PORTS[$i]}" --peers="${PEERS[$i]}" \
      --replicate-delay-prefix=chain-a- --replicate-delay=150ms \
      >"${OUT_DIR}/node$((i+1))-${mode}.log" 2>&1 &
    PIDS+=("$!")
  done
  sleep 1.5
  local ready=0
  for p in "${PORTS[@]}"; do
    if go run ./scripts/wait_grpc.go "localhost:${p}" >/dev/null 2>&1; then
      ready=$((ready+1))
    fi
  done
  [[ "$ready" -eq 3 ]]
}

# Aggregate throughput/latency across per-run rows (one row per seed).
# CSV columns: mode,workload,seed,peer_delay_ms,operations,...,throughput(col10),p50(col11),p95(col12),p99(col13)
bench_throughput_summary() {
  local csv="$1"
  awk -F, '
    NR==1 { next }
    {
      k=$2
      n[k]++
      tp[k]+=$10
      tp2[k]+=$10*$10
    }
    END {
      for (k in n) {
        mean=tp[k]/n[k]
        if (n[k]>1) {
          var=(tp2[k]-tp[k]*tp[k]/n[k])/(n[k]-1)
          if (var<0) var=0
          std=sqrt(var)
          printf "%s: %.0f ± %.0f ops/s (%d runs)\n", k, mean, std, n[k]
        } else {
          printf "%s: %.0f ops/s (1 run)\n", k, mean
        }
      }
    }
  ' "$csv"
}

bench_latency_summary() {
  local csv="$1" mode="$2"
  awk -F, -v mode="$mode" '
    NR==1 { next }
    $1==mode {
      k=$2
      n[k]++
      p50[k]+=$11; p95[k]+=$12; p99[k]+=$13
    }
    END {
      for (k in n) {
        printf "%s|%s|%.0f|%.0f|%.0f\n", mode, k, p50[k]/n[k]/1000, p95[k]/n[k]/1000, p99[k]/n[k]/1000
      }
    }
  ' "$csv"
}

run_mode() {
  local mode="$1"
  record ""
  record "## Mode: ${mode}"
  record ""

  if ! start_cluster "$mode"; then
    fail "${mode}: cluster start"
    record "- **Cluster:** FAILED"
    return
  fi
  pass "${mode}: cluster started"

  # Benchmark (YCSB workloads, 5 seeds — one CSV row per run)
  local csv="${OUT_DIR}/summary_${mode}.csv"
  rm -f "$csv"
  for wl in "${WORKLOADS[@]}"; do
    for seed in "${SEEDS[@]}"; do
      if bin/bench --target=localhost:50051 \
        --workload="bench/workloads/${wl}.yaml" \
        --mode="$mode" --output="$csv" --seed="$seed" 2>/dev/null; then
        pass "${mode}/${wl}/seed${seed}"
      else
        fail "${mode}/${wl}/seed${seed}"
      fi
    done
  done

  # Checker
  if bin/checker --nodes=localhost:50051,localhost:50052,localhost:50053 \
      --key-prefix=user --key-count=100 --settle=5s >>"$REPORT" 2>&1; then
    pass "${mode}: convergence"
    record "- **Convergence:** CONSISTENT"
  else
    fail "${mode}: convergence"
    record "- **Convergence:** INCONSISTENT"
  fi

  # Staleness + causal chain (one CSV row per trial)
  local exp="${OUT_DIR}/experiments.csv"
  if bin/experiments --test=all --mode="$mode" --trials=20 \
      --output="$exp" >>"$REPORT" 2>&1; then
    pass "${mode}: experiments"
  else
    fail "${mode}: experiments"
  fi

  if [[ -f "$csv" ]]; then
    record ""
    record "### Throughput (ops/s) — ${mode}"
    record '```'
    bench_throughput_summary "$csv" | tee -a "$REPORT"
    record '```'
  fi
}

# ── Phase 0: build & unit test ──────────────────────────────────────────────
record "# kvstore Paper Experiment Report"
record ""
record "Generated: $(date -Iseconds)"
record ""

log "building..."
go build -o bin/node ./cmd/node
go build -o bin/bench ./cmd/bench
go build -o bin/checker ./cmd/checker
go build -o bin/experiments ./cmd/experiments

log "unit tests..."
if go test ./... -count=1 -timeout=120s 2>&1 | tee "${OUT_DIR}/unit_tests.log" | tail -5; then
  pass "unit tests"
  record "## Unit tests: PASS"
else
  fail "unit tests"
  record "## Unit tests: FAIL"
fi

record ""
record "## Environment"
record "- Go: $(go version)"
record "- OS: $(uname -srm)"
record "- Cluster: 3 nodes, localhost:50051-50053"
record ""

rm -f "${OUT_DIR}/experiments.csv"

# ── Phase 1–3: per-mode experiments ─────────────────────────────────────────
for mode in "${MODES[@]}"; do
  run_mode "$mode" || true
done

# ── Combined latency table (all modes × workloads) ─────────────────────────
record ""
record "## Latency (µs) — mean across runs"
record ""
record "| Mode | Workload | p50 | p95 | p99 |"
record "|------|----------|-----|-----|-----|"

for mode in "${MODES[@]}"; do
  csv="${OUT_DIR}/summary_${mode}.csv"
  if [[ -f "$csv" ]]; then
    while IFS='|' read -r m wl p50 p95 p99; do
      record "| ${m} | ${wl} | ${p50} | ${p95} | ${p99} |"
    done < <(bench_latency_summary "$csv" "$mode" | sort -t'|' -k2)
  fi
done

# ── Summary ─────────────────────────────────────────────────────────────────
record ""
record "## Summary"
record "- **Checks passed:** ${PASS}"
record "- **Checks failed:** ${FAIL}"
record ""
record "## Result files"
record "- \`${OUT_DIR}/summary_<mode>.csv\` — one row per (workload, seed) with throughput/latency"
record "- \`${OUT_DIR}/experiments.csv\` — one row per trial (staleness_ms or violation)"
record "- \`${OUT_DIR}/unit_tests.log\` — test output"

log "=============================="
log "PASS=${PASS}  FAIL=${FAIL}"
log "Report: ${REPORT}"

if [[ "$FAIL" -gt 0 ]]; then exit 1; fi
