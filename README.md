# Major KV Store

This project is a replicated in-memory key-value store with eventual, causal,
and quorum-based strong consistency modes. Clients and nodes communicate over
gRPC.

## Requirements

- Go 1.25 or later
- Docker and Docker Compose (optional, for starting the three-node cluster)

Run commands in this README from the project root.

## Build all binaries

```bash
mkdir -p bin
go build -o bin/node ./cmd/node
go build -o bin/bench ./cmd/bench
go build -o bin/checker ./cmd/checker
go build -o bin/experiments ./cmd/experiments
```

Every binary also supports Go's standard `-h` flag:

```bash
bin/node -h
bin/bench -h
bin/checker -h
bin/experiments -h
```

Go duration flags accept values such as `500ms`, `3s`, `2m`, and `1h`.

## `node`: run a KV-store node

```bash
bin/node [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--mode` | `eventual` | Consistency mode: `eventual`, `causal`, or `strong`. |
| `--port` | `50051` | TCP port on which the gRPC server listens. |
| `--peers` | empty | Comma-separated peer addresses. |
| `--replicate-delay-prefix` | empty | Delay replication only for keys beginning with this prefix. Intended for experiments. |
| `--replicate-delay` | `0` | Additional replication delay for matching keys. |
| `--peer-delay` | `0` | Simulated delay before every peer `Replicate` RPC. |
| `--pprof` | empty | Start the Go profiling HTTP server at this address, for example `localhost:6060`. |

Example: start three eventual-consistency nodes in separate terminals:

```bash
bin/node --mode=eventual --port=50051 --peers=localhost:50052,localhost:50053
bin/node --mode=eventual --port=50052 --peers=localhost:50051,localhost:50053
bin/node --mode=eventual --port=50053 --peers=localhost:50051,localhost:50052
```

Use the same `--mode` on every node in a cluster. To run a causal or strong
cluster, replace `eventual` in all three commands.

Example with simulated network latency and profiling:

```bash
bin/node \
  --mode=strong \
  --port=50051 \
  --peers=localhost:50052,localhost:50053 \
  --peer-delay=20ms \
  --pprof=localhost:6060
```

Alternatively, start the predefined three-node cluster with Docker Compose:

```bash
MODE=causal PEER_DELAY=5ms docker compose up --build
```

`MODE` defaults to `eventual`, and `PEER_DELAY` defaults to `0`.

## `bench`: load data and run a workload

The benchmark is a gRPC client. A node must already be running at `--target`.

```bash
bin/bench [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--target` | `localhost:50051` | Address of the node receiving benchmark requests. |
| `--workload` | `bench/workloads/workload_a.yaml` | Workload YAML file. |
| `--mode` | `eventual` | Mode label written to the CSV; this does not configure the server. |
| `--output` | `bench/results/summary.csv` | Summary CSV output path. Parent directories are created automatically. |
| `--seed` | `0` | Random-number seed. `0` selects a seed from the current time. |
| `--peer-delay-ms` | `0` | Server peer-delay label recorded in the CSV; this does not add delay itself. |
| `--skip-load` | `false` | Skip insertion of the initial `record_count` records. |
| `--timeout` | `5m` | Overall timeout covering load and measured operations. |

Example:

```bash
bin/bench \
  --target=localhost:50051 \
  --workload=bench/workloads/workload_a.yaml \
  --mode=causal \
  --output=bench/results/causal.csv \
  --seed=42 \
  --timeout=5m
```

The benchmark first loads keys such as `user0`, `user1`, and so on, unless
`--skip-load` is set. It then runs the operation mix from the YAML and records
throughput and latency statistics. The supplied workloads are:

| Workload | Read/update mix | Key distribution |
| --- | --- | --- |
| `workload_a.yaml` | 50% / 50% | Zipfian |
| `workload_b.yaml` | 95% / 5% | Zipfian |
| `workload_c.yaml` | 100% / 0% | Zipfian |
| `workload_write_heavy.yaml` | Defined in the YAML | Zipfian |
| `workload_uniform_a.yaml` | Defined in the YAML | Uniform |

The value supplied to `--mode` should match the mode used to start the nodes;
otherwise the CSV will be incorrectly labelled. Likewise,
`--peer-delay-ms=20` only records `20` in the results. Start the servers with
`--peer-delay=20ms` to actually simulate that delay.

## `checker`: verify replica convergence

The checker waits for replication to settle, reads a generated range of keys
from every listed node, and exits unsuccessfully if their values differ.

```bash
bin/checker [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--nodes` | `localhost:50051,localhost:50052,localhost:50053` | Comma-separated node addresses. |
| `--key-prefix` | `user` | Prefix used to generate keys to inspect. |
| `--key-count` | `100` | Number of keys to inspect, starting at index zero. |
| `--settle` | `3s` | Time allowed for replication to converge before comparison. |
| `--timeout` | `30s` | Overall checker timeout. |

Example:

```bash
bin/checker \
  --nodes=localhost:50051,localhost:50052,localhost:50053 \
  --key-prefix=user \
  --key-count=1000 \
  --settle=5s \
  --timeout=30s
```

With `--key-prefix=user --key-count=1000`, the checker examines `user0` through
`user999` on every node.

## `experiments`: staleness and causal-ordering tests

The experiments client requires at least two running nodes: an origin that
receives writes and a replica on which visibility is checked.

```bash
bin/experiments [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--test` | `all` | Experiment to run: `staleness`, `causal`, or `all`. |
| `--origin` | `localhost:50051` | Node to which experiment writes are sent. |
| `--replica` | `localhost:50052` | Node observed for replicated values. |
| `--mode` | `eventual` | Mode label written to output; this does not configure either server. |
| `--trials` | `30` | Number of trials per selected experiment. |
| `--output` | `bench/results/paper/experiments.csv` | Per-trial CSV output path. |

Example:

```bash
bin/experiments \
  --test=all \
  --origin=localhost:50051 \
  --replica=localhost:50052 \
  --mode=causal \
  --trials=50 \
  --output=bench/results/paper/causal_experiments.csv
```

The program has a fixed overall timeout of 10 minutes. The causal-ordering
experiment is designed to be paired with replication delay settings used by
the experiment scripts; see `scripts/run_network_delay_bench.sh` and
`analysis/run_delay_evaluation.sh` for complete examples.

## Helper programs

The files in `scripts/` below have a `//go:build ignore` directive and are
normally invoked directly with `go run` instead of being included in regular
package builds.

### `scripts/test_client.go`

Runs a Put/Get/Delete smoke test against one node:

```bash
go run scripts/test_client.go [address]
```

The address is optional and defaults to `localhost:50051`. This helper has no
named flags.

### `scripts/wait_grpc.go`

Checks whether a TCP connection can be established to a node:

```bash
go run scripts/wait_grpc.go [address]
```

The address is optional and defaults to `localhost:50051`. It prints `ready`
on success and exits with status 1 on failure.

## Common workflows

Run one benchmark and then check convergence:

```bash
MODE=eventual WORKLOAD=bench/workloads/workload_a.yaml ./scripts/run_bench.sh
```

Run all configured workloads with seeds 1 through 5 against an already running
cluster:

```bash
MODE=causal ./scripts/run_experiments.sh
```

When using these scripts, ensure the running cluster uses the same consistency
mode supplied through `MODE`.

## Evaluation matrices and run counts

A benchmark run is one complete invocation of `bin/bench` for a particular
mode, workload, seed, and (when applicable) injected network delay. It is not a
single key-value operation. For example, each supplied A/B/C workload performs
10,000 measured operations after loading 1,000 records.

The repository provides scripts for running the complete matrices:

| Evaluation | Calculation | Benchmark runs | Script |
| --- | --- | ---: | --- |
| Core | 3 modes × 3 workloads × 5 seeds | 45 | `analysis/run_core_evaluation.sh` |
| Paper | 3 modes × 5 workloads × 5 seeds | 75 | `scripts/run_paper_experiments.sh` |
| Network-delay robustness | 3 modes × 3 delays × 3 workloads × 5 seeds | 135 | `analysis/run_delay_evaluation.sh` |

Stop the Docker Compose cluster first because the evaluation scripts start
their own nodes on ports 50051–50053:

```bash
docker compose down
```

Run the 45-run core matrix:

```bash
bash analysis/run_core_evaluation.sh "$PWD" /tmp/core-evaluation
```

Run the 135-run delay matrix:

```bash
bash analysis/run_delay_evaluation.sh "$PWD" /tmp/delay-evaluation
```

The output directory for either analysis script must not already exist. This
prevents an earlier dataset from being overwritten or mixed with a new run.
If a directory from an earlier run exists, select a new name instead:

```bash
bash analysis/run_core_evaluation.sh "$PWD" /tmp/core-evaluation-2
bash analysis/run_delay_evaluation.sh "$PWD" /tmp/delay-evaluation-2
```

The 45-run core evaluation writes:

| Output | Contents |
| --- | --- |
| `/tmp/core-evaluation/benchmark.csv` | 45 benchmark result rows |
| `/tmp/core-evaluation/convergence.csv` | Checker result for each of the 45 runs |
| `/tmp/core-evaluation/correctness.csv` | Replication-staleness and causal-ordering trials |
| `/tmp/core-evaluation/logs/` | Per-run benchmark and correctness logs |
| `/tmp/core-evaluation/checker/` | Detailed checker logs |

The 135-run network-delay evaluation writes:

| Output | Contents |
| --- | --- |
| `/tmp/delay-evaluation/benchmark_delay.csv` | 135 benchmark result rows |
| `/tmp/delay-evaluation/convergence_delay.csv` | Checker result for each of the 135 runs |
| `/tmp/delay-evaluation/run_manifest.csv` | Every mode/delay/workload/seed combination executed |
| `/tmp/delay-evaluation/correctness_delay*.csv` | Correctness trials grouped by delay |
| `/tmp/delay-evaluation/logs/` | Per-run benchmark and correctness logs |
| `/tmp/delay-evaluation/checker/` | Detailed checker logs |

Confirm the number of benchmark rows after both evaluations finish. The header
row is excluded from each count:

```bash
awk 'END {print NR-1}' /tmp/core-evaluation/benchmark.csv
# Expected: 45

awk 'END {print NR-1}' /tmp/delay-evaluation/benchmark_delay.csv
# Expected: 135
```

These commands are evaluation suites rather than ordinary unit tests. The
135-run delay matrix will generally take considerably longer than the 45-run
core matrix.

Run the 75-run paper pipeline:

```bash
./scripts/run_paper_experiments.sh
```

The scripts build the required binaries, start a three-node cluster in each
mode, execute every matrix combination, and save CSV results and logs. Thus,
the matrices do not need to be launched one benchmark at a time.

## Checker and other tests

You do not normally need to run the checker separately when using the provided
workflows:

- `scripts/run_bench.sh` and `scripts/run_experiments.sh` run the checker after
  their benchmark workload or matrix completes.
- `analysis/run_core_evaluation.sh` and
  `analysis/run_delay_evaluation.sh` run the checker after every benchmark run
  and record the result in `convergence.csv` or `convergence_delay.csv`.
- `scripts/run_paper_experiments.sh` runs one convergence check after the
  benchmarks for each consistency mode.
- `scripts/e2e_test.sh` runs a smoke benchmark and convergence check in every
  consistency mode.

The checker is a post-run replica-convergence test: it waits for replication to
settle, reads the configured keys from all three nodes, and fails if their
values differ. To run it manually against an existing cluster:

```bash
bin/checker \
  --nodes=localhost:50051,localhost:50052,localhost:50053 \
  --key-prefix=user \
  --key-count=1000 \
  --settle=10s \
  --timeout=15s
```

The project also has tests beyond performance benchmarks:

```bash
# Unit and package-level integration tests
go test ./...

# Static analysis
go vet ./...

# End-to-end RPC, smoke-benchmark, and convergence tests in all three modes
docker compose down
./scripts/e2e_test.sh
```

The full evaluation pipelines additionally run the `experiments` binary for
replication-visibility/staleness trials and causal-ordering trials. These are
correctness experiments rather than throughput benchmarks.
