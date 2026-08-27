#!/usr/bin/env bash
# Throughput comparison across simulated network delays (0, 5ms, 20ms).
# Tests the hypothesis: strong mode pays quorum RTT; causal pays CPU overhead.
# With added peer delay, strong should degrade faster than causal.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OUT_DIR="${OUT_DIR:-bench/results/network_delay}"
REPORT="${OUT_DIR}/REPORT.md"
PORTS=(50051 50052 50053)
PEERS=(
  "localhost:50052,localhost:50053"
  "localhost:50051,localhost:50053"
  "localhost:50051,localhost:50052"
)
MODES=(eventual causal strong)
DELAYS=(0 5ms 20ms)
WORKLOADS=(workload_a workload_b workload_c workload_write_heavy workload_uniform_a)
SEEDS=(1 2 3 4 5)
PIDS=()

log() { printf '[netdelay] %s\n' "$*"; }

cleanup() {
  for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$OUT_DIR"
: > "$REPORT"

kill_ports() {
  for p in "${PORTS[@]}"; do fuser -k "${p}/tcp" 2>/dev/null || true; done
  sleep 0.5
}

start_cluster() {
  local mode="$1" peer_delay="$2"
  kill_ports
  PIDS=()
  for i in 0 1 2; do
    local extra=()
    if [[ "$i" -eq 0 ]]; then
      extra+=(--replicate-delay-prefix=chain-a- --replicate-delay=150ms)
    fi
    bin/node --mode="$mode" --port="${PORTS[$i]}" --peers="${PEERS[$i]}" \
      --peer-delay="$peer_delay" \
      "${extra[@]}" \
      >"${OUT_DIR}/node$((i+1))-${mode}-${peer_delay}.log" 2>&1 &
    PIDS+=("$!")
  done
  sleep 1.5
  for p in "${PORTS[@]}"; do
    go run ./scripts/wait_grpc.go "localhost:${p}" >/dev/null
  done
}

delay_label() {
  case "$1" in
    0) echo "0ms" ;;
    5ms) echo "5ms" ;;
    20ms) echo "20ms" ;;
    *) echo "$1" ;;
  esac
}

delay_ms() {
  case "$1" in
    0) echo 0 ;;
    5ms) echo 5 ;;
    20ms) echo 20 ;;
    *) echo 0 ;;
  esac
}

log "building..."
go build -o bin/node ./cmd/node
go build -o bin/bench ./cmd/bench

{
  echo "# Network Delay Benchmark Report"
  echo ""
  echo "Generated: $(date -Iseconds)"
  echo ""
  echo "Simulated peer RTT via \`--peer-delay\` on each Replicate RPC."
  echo "Strong mode blocks on quorum acks; causal/eventual fire-and-forget."
  echo ""
  echo "## Throughput (ops/s) — workload A, mean of ${#SEEDS[@]} seeds"
  echo ""
  echo "| Peer delay | Eventual | Causal | Strong |"
  echo "|------------|----------|--------|--------|"
} > "$REPORT"

for delay in "${DELAYS[@]}"; do
  label="$(delay_label "$delay")"
  dms="$(delay_ms "$delay")"
  log "=== peer delay ${label} ==="

  declare -A TP
  for mode in "${MODES[@]}"; do
    csv="${OUT_DIR}/summary_${mode}_${label}.csv"
    rm -f "$csv"
    start_cluster "$mode" "$delay"

    for wl in "${WORKLOADS[@]}"; do
      for seed in "${SEEDS[@]}"; do
        log "  ${mode} / ${wl} / seed=${seed} / delay=${label}"
        bin/bench \
          --target=localhost:50051 \
          --workload="bench/workloads/${wl}.yaml" \
          --mode="$mode" \
          --output="$csv" \
          --seed="$seed" \
          --peer-delay-ms="$dms"
      done
    done

    TP[$mode]=$(awk -F, 'NR>1 && $2=="workload_a" {s+=$10; n++} END{if(n) printf "%.0f", s/n; else print "—"}' "$csv")
  done

  echo "| ${label} | ${TP[eventual]:-—} | ${TP[causal]:-—} | ${TP[strong]:-—} |" >> "$REPORT"
done

{
  echo ""
  echo "## Result files"
  echo "- \`${OUT_DIR}/summary_<mode>_<delay>.csv\` — per-run rows with \`peer_delay_ms\` column"
  echo ""
  echo "## Interpretation"
  echo "- At 0ms (localhost): strong may match or beat causal (negligible quorum cost)."
  echo "- At 5–20ms: strong write latency grows with quorum RTT; causal CPU overhead dominates less."
  echo "- If causal throughput exceeds strong at higher delay, the network-bound vs CPU-bound hypothesis holds."
} >> "$REPORT"

log "Report: ${REPORT}"
