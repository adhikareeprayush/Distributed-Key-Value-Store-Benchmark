# Data quality and validation report

Overall status: PASS

- Core benchmark: 45 rows; 450,000 operations; 0 errors.
- Delay benchmark: 135 rows; 1,350,000 operations; 0 errors.
- Delay correctness: 720 trial rows.
- Core convergence: 45/45 primary checks passed.
- Delay convergence: 132/135 primary checks passed; all 6/6 separately labeled confirmation checks passed.
- core: verified 121 raw-file checksums; 0 failures.
- delay: verified 332 raw-file checksums; 0 failures.
- convergence-confirmation: verified 29 raw-file checksums; 0 failures.

## Known measurement boundaries

- Run-level latency summaries combine reads and writes; the raw harness does not expose operation-specific latency samples.
- The delay flag sleeps before each outgoing Replicate RPC; it is controlled one-way delay, not a measured network RTT.
- The delay study loads once per fresh mode/delay cluster and uses the preloaded state for the remaining runs; load time is outside every measured interval.
- Execution order is fixed rather than randomized, the CPU governor was powersave, and the host was not otherwise isolated.
- Five repetitions support confidence intervals but leave low power for normality and significance tests.
- Staleness samples use integer milliseconds and a 5 ms polling interval; sub-millisecond values are quantized to zero.
- Checker elapsed time includes scanning 1,000 keys on three nodes and is not pure replication convergence latency.
- The causal-chain micro-test validates one targeted anomaly pattern, not the complete causal-consistency state space.

## Validation issues

- None.
