#!/usr/bin/env bash
# Capture a CPU profile from the causal-mode origin node during a benchmark run.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OUT_DIR="${OUT_DIR:-bench/results/paper}"
PPROF_ADDR="${PPROF_ADDR:-localhost:6060}"
PROFILE_SEC="${PROFILE_SEC:-15}"
WORKLOAD="${WORKLOAD:-bench/workloads/workload_a.yaml}"
PORTS=(50051 50052 50053)
PEERS=(
  "localhost:50052,localhost:50053"
  "localhost:50051,localhost:50053"
  "localhost:50051,localhost:50052"
)
PIDS=()

log() { printf '[profile] %s\n' "$*"; }

cleanup() {
  for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  fuser -k 6060/tcp 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$OUT_DIR"

kill_ports() {
  for p in "${PORTS[@]}"; do fuser -k "${p}/tcp" 2>/dev/null || true; done
  fuser -k 6060/tcp 2>/dev/null || true
  sleep 0.5
}

log "building..."
go build -o bin/node ./cmd/node
go build -o bin/bench ./cmd/bench

kill_ports
PIDS=()
for i in 0 1 2; do
  pprof_flag=()
  if [[ "$i" -eq 0 ]]; then
    pprof_flag=(--pprof="0.0.0.0:6060")
  fi
  bin/node --mode=causal --port="${PORTS[$i]}" --peers="${PEERS[$i]}" \
    "${pprof_flag[@]}" \
    >"${OUT_DIR}/profile-node$((i+1)).log" 2>&1 &
  PIDS+=("$!")
done
sleep 1.5
for p in "${PORTS[@]}"; do
  go run ./scripts/wait_grpc.go "localhost:${p}" >/dev/null
done

PROF="${OUT_DIR}/causal_cpu.prof"
TOP="${OUT_DIR}/causal_cpu_top.txt"
REPORT="${OUT_DIR}/PROFILE.md"

log "starting benchmark + ${PROFILE_SEC}s CPU profile..."
(
  sleep 2
  curl -sf "http://${PPROF_ADDR}/debug/pprof/profile?seconds=${PROFILE_SEC}" -o "$PROF"
) &
PROF_PID=$!

bin/bench \
  --target=localhost:50051 \
  --workload="$WORKLOAD" \
  --mode=causal \
  --output="${OUT_DIR}/profile_bench.csv" \
  --seed=42

wait "$PROF_PID"

if [[ ! -s "$PROF" ]]; then
  log "ERROR: profile not captured (is pprof listening on ${PPROF_ADDR}?)"
  exit 1
fi

go tool pprof -top -nodecount=25 "$PROF" > "$TOP" 2>&1

{
  echo "# Causal Mode CPU Profile"
  echo ""
  echo "Generated: $(date -Iseconds)"
  echo ""
  echo "- Workload: \`${WORKLOAD}\`"
  echo "- Profile duration: ${PROFILE_SEC}s during benchmark"
  echo "- Raw profile: \`causal_cpu.prof\`"
  echo ""
  echo "## Top CPU samples"
  echo '```'
  cat "$TOP"
  echo '```'
  echo ""
  echo "## How to explore"
  echo '```bash'
  echo "go tool pprof -http=:8081 ${PROF}"
  echo '```'
} > "$REPORT"

log "Profile:  ${PROF}"
log "Top list: ${TOP}"
log "Report:   ${REPORT}"
