#!/usr/bin/env python3
"""Validate immutable KV-store evidence and generate report tables/figures."""

from __future__ import annotations

import argparse
import hashlib
import math
from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
from scipy import stats


MODES = ["eventual", "causal", "strong"]
WORKLOADS = ["workload_a", "workload_b", "workload_c"]
DELAYS = [0, 5, 20]
SEEDS = [1, 2, 3, 4, 5]
MODE_LABEL = {"eventual": "Eventual", "causal": "Causal", "strong": "Strong"}
WORKLOAD_LABEL = {
    "workload_a": "A — 50/50",
    "workload_b": "B — 95/5",
    "workload_c": "C — read-only",
}
COLORS = {"eventual": "#4C78A8", "causal": "#F58518", "strong": "#54A24B"}
MARKERS = {"eventual": "o", "causal": "s", "strong": "^"}


def mean_ci(values: pd.Series) -> tuple[float, float, float]:
    values = pd.to_numeric(values, errors="raise").astype(float)
    n = len(values)
    mean = float(values.mean())
    sd = float(values.std(ddof=1)) if n > 1 else 0.0
    half = float(stats.t.ppf(0.975, n - 1) * sd / math.sqrt(n)) if n > 1 else 0.0
    return mean, sd, half


def harness_percentile(values: pd.Series, p: float) -> float:
    vals = np.sort(pd.to_numeric(values.dropna(), errors="raise").to_numpy(dtype=float))
    if len(vals) == 0:
        return float("nan")
    return float(vals[int((len(vals) - 1) * p)])


def wilson(successes: int, trials: int, z: float = 1.959963984540054) -> tuple[float, float]:
    if trials == 0:
        return float("nan"), float("nan")
    p = successes / trials
    den = 1 + z * z / trials
    center = (p + z * z / (2 * trials)) / den
    half = z * math.sqrt(p * (1 - p) / trials + z * z / (4 * trials * trials)) / den
    return max(0.0, center - half), min(1.0, center + half)


def holm_adjust(p_values: list[float]) -> list[float]:
    m = len(p_values)
    order = np.argsort(p_values)
    adjusted = np.empty(m, dtype=float)
    running = 0.0
    for rank, idx in enumerate(order):
        value = min(1.0, (m - rank) * p_values[idx])
        running = max(running, value)
        adjusted[idx] = running
    return adjusted.tolist()


def verify_sha_manifest(manifest_path: Path) -> tuple[int, list[str]]:
    checked = 0
    failures: list[str] = []
    for line in manifest_path.read_text().splitlines():
        if not line.strip():
            continue
        digest, raw_path = line.split("  ", 1)
        path = Path(raw_path)
        if not path.exists():
            failures.append(f"missing: {path}")
            continue
        h = hashlib.sha256()
        with path.open("rb") as handle:
            for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                h.update(chunk)
        checked += 1
        if h.hexdigest() != digest:
            failures.append(f"hash mismatch: {path}")
    return checked, failures


def validate_benchmark(df: pd.DataFrame, delays: list[int], name: str) -> list[str]:
    issues: list[str] = []
    expected_rows = len(MODES) * len(WORKLOADS) * len(SEEDS) * len(delays)
    if len(df) != expected_rows:
        issues.append(f"{name}: expected {expected_rows} rows, found {len(df)}")
    key = ["mode", "workload", "seed", "peer_delay_ms"]
    if int(df.duplicated(key).sum()) != 0:
        issues.append(f"{name}: duplicate configuration rows found")
    expected = {(m, w, s, d) for m in MODES for w in WORKLOADS for s in SEEDS for d in delays}
    observed = set(map(tuple, df[key].itertuples(index=False, name=None)))
    if observed != expected:
        issues.append(f"{name}: configuration grid is incomplete or contains unexpected cells")
    if not df["operations"].eq(10000).all():
        issues.append(f"{name}: operations is not 10,000 in every row")
    if not (df["reads"] + df["writes"]).eq(df["operations"]).all():
        issues.append(f"{name}: reads + writes does not equal operations")
    if int(df["errors"].sum()) != 0:
        issues.append(f"{name}: non-zero operation errors found")
    if df.isna().any().any():
        issues.append(f"{name}: unexpected null values found")
    return issues


def summarize_benchmark(df: pd.DataFrame, delay: bool) -> pd.DataFrame:
    groups = ["peer_delay_ms", "mode", "workload"] if delay else ["mode", "workload"]
    rows = []
    for key, group in df.groupby(groups, sort=False):
        if not isinstance(key, tuple):
            key = (key,)
        row = dict(zip(groups, key))
        row["runs"] = len(group)
        for source, label, scale in [
            ("throughput_ops", "throughput_ops", 1.0),
            ("latency_p50_ns", "latency_p50_ms", 1e-6),
            ("latency_p95_ns", "latency_p95_ms", 1e-6),
            ("latency_p99_ns", "latency_p99_ms", 1e-6),
            ("latency_avg_ns", "latency_avg_ms", 1e-6),
        ]:
            mean, sd, ci = mean_ci(group[source])
            row[f"{label}_mean"] = mean * scale
            row[f"{label}_sd"] = sd * scale
            row[f"{label}_ci95"] = ci * scale
        row["operations"] = int(group["operations"].sum())
        row["errors"] = int(group["errors"].sum())
        rows.append(row)
    return pd.DataFrame(rows).sort_values(groups).reset_index(drop=True)


def load_correctness(delay_dir: Path) -> pd.DataFrame:
    frames = []
    for delay in DELAYS:
        frame = pd.read_csv(delay_dir / f"correctness_delay{delay}.csv")
        frame["peer_delay_ms"] = delay
        frames.append(frame)
    return pd.concat(frames, ignore_index=True)


def summarize_correctness(df: pd.DataFrame) -> tuple[pd.DataFrame, pd.DataFrame]:
    stale_rows = []
    causal_rows = []
    for (delay, mode, test), group in df.groupby(["peer_delay_ms", "mode", "test"]):
        if test == "staleness":
            samples = pd.to_numeric(group["staleness_ms"], errors="coerce")
            visible = int(samples.notna().sum())
            trials = len(group)
            stale_rows.append({
                "peer_delay_ms": delay,
                "mode": mode,
                "trials": trials,
                "visible": visible,
                "timeouts": trials - visible,
                "mean_ms": float(samples.mean()),
                "median_ms": float(samples.median()),
                "p95_ms": harness_percentile(samples, 0.95),
                "max_ms": float(samples.max()),
            })
        elif test == "causal_chain":
            vals = pd.to_numeric(group["violation"], errors="raise").astype(int)
            violations = int(vals.sum())
            low, high = wilson(violations, len(vals))
            causal_rows.append({
                "peer_delay_ms": delay,
                "mode": mode,
                "trials": len(vals),
                "violations": violations,
                "violation_rate_pct": violations / len(vals) * 100,
                "wilson_low_pct": low * 100,
                "wilson_high_pct": high * 100,
            })
    return (
        pd.DataFrame(stale_rows).sort_values(["peer_delay_ms", "mode"]),
        pd.DataFrame(causal_rows).sort_values(["peer_delay_ms", "mode"]),
    )


def paired_tests(core: pd.DataFrame, delay: pd.DataFrame) -> pd.DataFrame:
    rows = []
    comparisons = [("eventual", "causal"), ("eventual", "strong"), ("causal", "strong")]
    for workload in WORKLOADS:
        part = core[core["workload"].eq(workload)]
        pivot = part.pivot(index="seed", columns="mode", values="throughput_ops")
        for baseline, comparison in comparisons:
            a, b = pivot[baseline], pivot[comparison]
            result = stats.ttest_rel(b, a)
            diff = b - a
            rows.append({
                "study": "core",
                "peer_delay_ms": 0,
                "workload": workload,
                "baseline": baseline,
                "comparison": comparison,
                "mean_change_pct": (float(b.mean()) / float(a.mean()) - 1) * 100,
                "mean_difference_ops": float(diff.mean()),
                "paired_cohen_dz": float(diff.mean() / diff.std(ddof=1)) if diff.std(ddof=1) else float("inf"),
                "p_value": float(result.pvalue),
            })
    core_count = len(rows)
    adjusted = holm_adjust([row["p_value"] for row in rows])
    for row, value in zip(rows, adjusted):
        row["holm_p_value"] = value

    for delay_ms in [5, 20]:
        local_rows = []
        for workload in WORKLOADS:
            part = delay[(delay["peer_delay_ms"].eq(delay_ms)) & delay["workload"].eq(workload)]
            pivot = part.pivot(index="seed", columns="mode", values="throughput_ops")
            for baseline, comparison in [("eventual", "strong"), ("causal", "strong")]:
                a, b = pivot[baseline], pivot[comparison]
                result = stats.ttest_rel(b, a)
                diff = b - a
                local_rows.append({
                    "study": "delay",
                    "peer_delay_ms": delay_ms,
                    "workload": workload,
                    "baseline": baseline,
                    "comparison": comparison,
                    "mean_change_pct": (float(b.mean()) / float(a.mean()) - 1) * 100,
                    "mean_difference_ops": float(diff.mean()),
                    "paired_cohen_dz": float(diff.mean() / diff.std(ddof=1)) if diff.std(ddof=1) else float("inf"),
                    "p_value": float(result.pvalue),
                })
        local_adjusted = holm_adjust([row["p_value"] for row in local_rows])
        for row, value in zip(local_rows, local_adjusted):
            row["holm_p_value"] = value
        rows.extend(local_rows)

    assert core_count == 9
    return pd.DataFrame(rows)


def set_plot_style() -> None:
    plt.rcParams.update({
        "font.family": "DejaVu Sans",
        "font.size": 10,
        "axes.titlesize": 11,
        "axes.labelsize": 10,
        "legend.fontsize": 9,
        "figure.titlesize": 14,
        "axes.spines.top": False,
        "axes.spines.right": False,
        "axes.grid": True,
        "axes.grid.axis": "y",
        "grid.alpha": 0.25,
        "axes.axisbelow": True,
    })


def save_both(fig: plt.Figure, graphics_dir: Path, stem: str) -> None:
    for suffix in [".pdf", ".png"]:
        path = graphics_dir / f"{stem}{suffix}"
        if path.exists():
            raise FileExistsError(f"refusing to overwrite existing figure: {path}")
    fig.savefig(graphics_dir / f"{stem}.pdf", bbox_inches="tight")
    fig.savefig(graphics_dir / f"{stem}.png", dpi=320, bbox_inches="tight", facecolor="white")
    plt.close(fig)


def plot_core_throughput(summary: pd.DataFrame, graphics_dir: Path) -> None:
    fig, ax = plt.subplots(figsize=(8.2, 4.8))
    x = np.arange(len(WORKLOADS))
    width = 0.24
    for i, mode in enumerate(MODES):
        part = summary[summary["mode"].eq(mode)].set_index("workload").loc[WORKLOADS]
        ax.bar(x + (i - 1) * width, part["throughput_ops_mean"], width,
               yerr=part["throughput_ops_ci95"], capsize=3, color=COLORS[mode],
               label=MODE_LABEL[mode], edgecolor="white", linewidth=0.6)
    ax.set_xticks(x, [WORKLOAD_LABEL[w] for w in WORKLOADS])
    ax.set_ylabel("Throughput (operations/s)")
    ax.set_ylim(bottom=0)
    ax.legend(ncol=3, frameon=False, loc="upper left")
    fig.suptitle("Throughput across the three workload mixes", y=1.02, fontweight="bold")
    fig.text(0.5, 0.965, "Mean of five seeded runs; 10,000 operations/run; error bars show 95% t confidence intervals",
             ha="center", va="top", fontsize=9, color="#444444")
    fig.tight_layout(rect=[0, 0, 1, 0.92])
    save_both(fig, graphics_dir, "results_core_throughput")


def plot_core_latency(summary: pd.DataFrame, graphics_dir: Path) -> None:
    fig, axes = plt.subplots(1, 3, figsize=(10.4, 4.5), sharey=True)
    metrics = [("latency_p50_ms_mean", "p50", "#9ecae9"),
               ("latency_p95_ms_mean", "p95", "#4292c6"),
               ("latency_p99_ms_mean", "p99", "#08519c")]
    x = np.arange(len(MODES))
    width = 0.23
    for ax, workload in zip(axes, WORKLOADS):
        part = summary[summary["workload"].eq(workload)].set_index("mode").loc[MODES]
        for i, (column, label, color) in enumerate(metrics):
            ax.bar(x + (i - 1) * width, part[column], width, label=label,
                   color=color, edgecolor="white", linewidth=0.5)
        ax.set_xticks(x, [MODE_LABEL[m] for m in MODES], rotation=20)
        ax.set_title(WORKLOAD_LABEL[workload])
        ax.set_ylim(bottom=0)
    axes[0].set_ylabel("Combined operation latency (ms)")
    axes[-1].legend(frameon=False, loc="upper right")
    fig.suptitle("Latency percentiles by mode and workload", y=1.03, fontweight="bold")
    fig.text(0.5, 0.97, "Mean of run-level percentiles over five seeds; read and write operations are combined",
             ha="center", va="top", fontsize=9, color="#444444")
    fig.tight_layout(rect=[0, 0, 1, 0.92])
    save_both(fig, graphics_dir, "results_core_latency")


def plot_delay_metric(summary: pd.DataFrame, graphics_dir: Path, metric: str, ci: str,
                      stem: str, title: str, ylabel: str, log_y: bool = False) -> None:
    fig, axes = plt.subplots(1, 3, figsize=(10.5, 4.5), sharey=True)
    for ax, workload in zip(axes, WORKLOADS):
        for mode in MODES:
            part = summary[(summary["workload"].eq(workload)) & summary["mode"].eq(mode)].sort_values("peer_delay_ms")
            ax.errorbar(part["peer_delay_ms"], part[metric], yerr=part[ci], label=MODE_LABEL[mode],
                        color=COLORS[mode], marker=MARKERS[mode], linewidth=2, capsize=3)
        ax.set_xticks(DELAYS)
        ax.set_xlabel("Injected peer delay (ms)")
        ax.set_title(WORKLOAD_LABEL[workload])
        if not log_y:
            ax.set_ylim(bottom=0)
        else:
            ax.set_yscale("log")
    axes[0].set_ylabel(ylabel)
    axes[-1].legend(frameon=False, loc="best")
    fig.suptitle(title, y=1.03, fontweight="bold")
    subtitle = "Mean of five seeded runs; error bars show 95% t confidence intervals"
    if log_y:
        subtitle += "; logarithmic y-axis"
    fig.text(0.5, 0.97, subtitle, ha="center", va="top", fontsize=9, color="#444444")
    fig.tight_layout(rect=[0, 0, 1, 0.92])
    save_both(fig, graphics_dir, stem)


def plot_staleness_ecdf(correctness: pd.DataFrame, graphics_dir: Path) -> None:
    fig, axes = plt.subplots(1, 3, figsize=(10.4, 4.4), sharex=True, sharey=True)
    for ax, delay in zip(axes, DELAYS):
        for mode in MODES:
            part = correctness[(correctness["peer_delay_ms"].eq(delay)) & correctness["mode"].eq(mode)
                               & correctness["test"].eq("staleness")]
            vals = np.sort(pd.to_numeric(part["staleness_ms"], errors="coerce").dropna().to_numpy())
            y = np.arange(1, len(vals) + 1) / len(vals) * 100
            ax.step(vals, y, where="post", color=COLORS[mode], label=MODE_LABEL[mode], linewidth=2)
        ax.set_title(f"{delay} ms delay")
        ax.set_xlabel("Post-ack visibility time (ms)")
        ax.set_xlim(-0.5, 30)
        ax.set_ylim(0, 102)
    axes[0].set_ylabel("Trials visible (%)")
    axes[-1].legend(frameon=False, loc="lower right")
    fig.suptitle("Replica visibility distributions under injected delay", y=1.03, fontweight="bold")
    fig.text(0.5, 0.97, "50 write-then-poll trials per mode and delay; 5 ms polling interval; integer-millisecond samples",
             ha="center", va="top", fontsize=9, color="#444444")
    fig.tight_layout(rect=[0, 0, 1, 0.92])
    save_both(fig, graphics_dir, "results_staleness_ecdf")


def plot_causal(causal: pd.DataFrame, graphics_dir: Path) -> None:
    fig, ax = plt.subplots(figsize=(8.2, 4.8))
    x = np.arange(len(DELAYS))
    width = 0.24
    for i, mode in enumerate(MODES):
        part = causal[causal["mode"].eq(mode)].set_index("peer_delay_ms").loc[DELAYS]
        values = part["violation_rate_pct"].to_numpy()
        lower = np.maximum(0.0, values - part["wilson_low_pct"].to_numpy())
        upper = np.maximum(0.0, part["wilson_high_pct"].to_numpy() - values)
        ax.bar(x + (i - 1) * width, values, width, color=COLORS[mode], label=MODE_LABEL[mode],
               yerr=np.vstack([lower, upper]), capsize=3, edgecolor="white", linewidth=0.6)
    ax.set_xticks(x, [f"{d} ms" for d in DELAYS])
    ax.set_xlabel("Injected peer delay")
    ax.set_ylabel("Causal-chain violations (%)")
    ax.set_ylim(0, 112)
    ax.legend(frameon=False, ncol=3, loc="upper right")
    fig.suptitle("Causal-ordering stress test separates the modes", y=1.02, fontweight="bold")
    fig.text(0.5, 0.965, "30 read-then-write chains per cell; 150 ms extra delay on predecessor keys; 95% Wilson intervals",
             ha="center", va="top", fontsize=9, color="#444444")
    fig.tight_layout(rect=[0, 0, 1, 0.92])
    save_both(fig, graphics_dir, "results_causal_violations")


def plot_convergence(summary: pd.DataFrame, graphics_dir: Path) -> None:
    fig, ax = plt.subplots(figsize=(8.2, 4.8))
    x = np.arange(len(DELAYS))
    width = 0.24
    for i, mode in enumerate(MODES):
        part = summary[summary["mode"].eq(mode)].set_index("peer_delay_ms").loc[DELAYS]
        values = part["pass_rate_pct"].to_numpy()
        bars = ax.bar(x + (i - 1) * width, values, width, color=COLORS[mode],
                      label=MODE_LABEL[mode], edgecolor="white", linewidth=0.6)
        for bar, value in zip(bars, values):
            ax.text(bar.get_x() + bar.get_width() / 2, value + 1.2, f"{value:.1f}",
                    ha="center", va="bottom", fontsize=8)
    ax.set_xticks(x, [f"{d} ms" for d in DELAYS])
    ax.set_xlabel("Injected peer delay")
    ax.set_ylabel("Primary checks passing (%)")
    ax.set_ylim(0, 108)
    ax.legend(frameon=False, ncol=3, loc="lower left")
    fig.suptitle("Full-replica convergence checks", y=1.02, fontweight="bold")
    fig.text(0.5, 0.965, "15 checks per mode and delay; three primary timeouts were retained and passed separate confirmation",
             ha="center", va="top", fontsize=9, color="#444444")
    fig.tight_layout(rect=[0, 0, 1, 0.92])
    save_both(fig, graphics_dir, "results_convergence")


def plot_workload_mix(quality: pd.DataFrame, graphics_dir: Path) -> None:
    fig, ax = plt.subplots(figsize=(7.6, 4.5))
    x = np.arange(len(WORKLOADS))
    reads = quality.set_index("workload").loc[WORKLOADS]["actual_read_pct"].to_numpy()
    writes = quality.set_index("workload").loc[WORKLOADS]["actual_write_pct"].to_numpy()
    ax.bar(x, reads, 0.58, label="Reads", color="#4C78A8")
    ax.bar(x, writes, 0.58, bottom=reads, label="Updates", color="#E45756")
    for i, (r, w) in enumerate(zip(reads, writes)):
        ax.text(x[i], r / 2.0, f"{r:.2f}%", ha="center", va="center",
                color="white", fontsize=9, fontweight="bold")
        if w >= 3.0:
            ax.text(x[i], r + w / 2.0, f"{w:.2f}%", ha="center", va="center",
                    color="white", fontsize=9, fontweight="bold")
        elif w > 0:
            ax.text(x[i], min(r + w + 3.5, 97.0), f"{w:.2f}%", ha="center", va="bottom",
                    color="#E45756", fontsize=9, fontweight="bold")
    ax.set_xticks(x, [WORKLOAD_LABEL[w] for w in WORKLOADS])
    ax.set_ylabel("Executed operations (%)")
    ax.set_ylim(0, 100)
    ax.legend(frameon=False, ncol=2, loc="lower right")
    fig.suptitle("Observed workload mixes match their configured targets", y=1.02, fontweight="bold")
    fig.text(0.5, 0.965, "Core matrix totals across 15 runs per workload; 150,000 operations per workload",
             ha="center", va="top", fontsize=9, color="#444444")
    fig.tight_layout(rect=[0, 0, 1, 0.92])
    save_both(fig, graphics_dir, "results_workload_mix")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("evidence_root", type=Path)
    parser.add_argument("derived_dir", type=Path)
    parser.add_argument("graphics_dir", type=Path)
    args = parser.parse_args()

    evidence = args.evidence_root.resolve()
    core_dir = evidence / "core"
    delay_dir = evidence / "delay"
    confirm_dir = evidence / "convergence-confirmation"
    derived = args.derived_dir.resolve()
    graphics = args.graphics_dir.resolve()
    if derived.exists() and any(derived.iterdir()):
        raise FileExistsError(f"refusing to overwrite non-empty derived directory: {derived}")
    derived.mkdir(parents=True, exist_ok=True)
    graphics.mkdir(parents=True, exist_ok=True)

    core = pd.read_csv(core_dir / "benchmark.csv")
    delay = pd.read_csv(delay_dir / "benchmark_delay.csv")
    core_convergence = pd.read_csv(core_dir / "convergence.csv")
    delay_convergence = pd.read_csv(delay_dir / "convergence_delay.csv")
    confirmation = pd.read_csv(confirm_dir / "confirmation.csv")
    correctness = load_correctness(delay_dir)

    issues = validate_benchmark(core, [0], "core benchmark")
    issues += validate_benchmark(delay, DELAYS, "delay benchmark")
    if len(core_convergence) != 45 or core_convergence.duplicated(["mode", "workload", "seed"]).any():
        issues.append("core convergence grid is incomplete or duplicated")
    if len(delay_convergence) != 135 or delay_convergence.duplicated(
            ["mode", "peer_delay_ms", "workload", "seed"]).any():
        issues.append("delay convergence grid is incomplete or duplicated")
    if len(correctness) != 720:
        issues.append(f"delay correctness expected 720 rows, found {len(correctness)}")
    expected_correctness = {("staleness", 50), ("causal_chain", 30)}
    for (_, _, test), group in correctness.groupby(["peer_delay_ms", "mode", "test"]):
        expected_n = dict(expected_correctness)[test]
        if len(group) != expected_n:
            issues.append(f"correctness cell {test} expected {expected_n}, found {len(group)}")
    if len(confirmation) != 6 or not confirmation["pass"].eq(1).all():
        issues.append("confirmation audit did not contain six passing checks")

    checksum_lines = []
    for directory in [core_dir, delay_dir, confirm_dir]:
        checked, failures = verify_sha_manifest(directory / "RAW_SHA256SUMS.txt")
        checksum_lines.append((directory.name, checked, failures))
        issues.extend([f"{directory.name}: {failure}" for failure in failures])
        if (directory / "source_integrity.txt").read_text().strip() != "unchanged":
            issues.append(f"{directory.name}: source integrity flag is not unchanged")

    core_summary = summarize_benchmark(core, delay=False)
    delay_summary = summarize_benchmark(delay, delay=True)

    workload_quality = []
    expected_read = {"workload_a": 50.0, "workload_b": 95.0, "workload_c": 100.0}
    for workload in WORKLOADS:
        part = core[core["workload"].eq(workload)]
        read_pct = float(part["reads"].sum() / part["operations"].sum() * 100)
        workload_quality.append({
            "workload": workload,
            "expected_read_pct": expected_read[workload],
            "actual_read_pct": read_pct,
            "actual_write_pct": 100 - read_pct,
            "operations": int(part["operations"].sum()),
            "errors": int(part["errors"].sum()),
        })
    workload_quality_df = pd.DataFrame(workload_quality)

    stale_summary, causal_summary = summarize_correctness(correctness)
    convergence_rows = []
    for (delay_ms, mode), group in delay_convergence.groupby(["peer_delay_ms", "mode"]):
        passes = int(group["pass"].sum())
        low, high = wilson(passes, len(group))
        convergence_rows.append({
            "peer_delay_ms": delay_ms,
            "mode": mode,
            "checks": len(group),
            "passes": passes,
            "pass_rate_pct": passes / len(group) * 100,
            "wilson_low_pct": low * 100,
            "wilson_high_pct": high * 100,
            "mean_elapsed_ms": float(group["elapsed_ms"].mean()),
            "max_elapsed_ms": int(group["elapsed_ms"].max()),
        })
    convergence_summary = pd.DataFrame(convergence_rows).sort_values(["peer_delay_ms", "mode"])
    tests = paired_tests(core, delay)

    core_summary.to_csv(derived / "core_summary.csv", index=False)
    delay_summary.to_csv(derived / "delay_summary.csv", index=False)
    workload_quality_df.to_csv(derived / "workload_quality.csv", index=False)
    stale_summary.to_csv(derived / "staleness_summary.csv", index=False)
    causal_summary.to_csv(derived / "causal_summary.csv", index=False)
    convergence_summary.to_csv(derived / "convergence_summary.csv", index=False)
    confirmation.to_csv(derived / "convergence_confirmation.csv", index=False)
    tests.to_csv(derived / "statistical_tests.csv", index=False)

    report = [
        "# Data quality and validation report",
        "",
        f"Overall status: {'PASS' if not issues else 'FAIL'}",
        "",
        f"- Core benchmark: {len(core)} rows; {int(core['operations'].sum()):,} operations; {int(core['errors'].sum())} errors.",
        f"- Delay benchmark: {len(delay)} rows; {int(delay['operations'].sum()):,} operations; {int(delay['errors'].sum())} errors.",
        f"- Delay correctness: {len(correctness)} trial rows.",
        f"- Core convergence: {int(core_convergence['pass'].sum())}/{len(core_convergence)} primary checks passed.",
        f"- Delay convergence: {int(delay_convergence['pass'].sum())}/{len(delay_convergence)} primary checks passed; all 6/6 separately labeled confirmation checks passed.",
    ]
    for name, checked, failures in checksum_lines:
        report.append(f"- {name}: verified {checked} raw-file checksums; {len(failures)} failures.")
    report.extend([
        "",
        "## Known measurement boundaries",
        "",
        "- Run-level latency summaries combine reads and writes; the raw harness does not expose operation-specific latency samples.",
        "- The delay flag sleeps before each outgoing Replicate RPC; it is controlled one-way delay, not a measured network RTT.",
        "- The delay study loads once per fresh mode/delay cluster and uses the preloaded state for the remaining runs; load time is outside every measured interval.",
        "- Execution order is fixed rather than randomized, the CPU governor was powersave, and the host was not otherwise isolated.",
        "- Five repetitions support confidence intervals but leave low power for normality and significance tests.",
        "- Staleness samples use integer milliseconds and a 5 ms polling interval; sub-millisecond values are quantized to zero.",
        "- Checker elapsed time includes scanning 1,000 keys on three nodes and is not pure replication convergence latency.",
        "- The causal-chain micro-test validates one targeted anomaly pattern, not the complete causal-consistency state space.",
        "",
        "## Validation issues",
        "",
    ])
    report.extend([f"- {issue}" for issue in issues] if issues else ["- None."])
    (derived / "quality_report.md").write_text("\n".join(report) + "\n")

    if issues:
        raise SystemExit("validation failed; see quality_report.md")

    set_plot_style()
    plot_core_throughput(core_summary, graphics)
    plot_core_latency(core_summary, graphics)
    plot_delay_metric(delay_summary, graphics, "throughput_ops_mean", "throughput_ops_ci95",
                      "results_delay_throughput", "Throughput response to injected peer delay",
                      "Throughput (operations/s)")
    plot_delay_metric(delay_summary, graphics, "latency_p99_ms_mean", "latency_p99_ms_ci95",
                      "results_delay_p99", "Tail latency response to injected peer delay",
                      "Combined-operation p99 latency (ms)", log_y=True)
    plot_staleness_ecdf(correctness, graphics)
    plot_causal(causal_summary, graphics)
    plot_convergence(convergence_summary, graphics)
    plot_workload_mix(workload_quality_df, graphics)

    print(f"validated evidence and wrote derived outputs to {derived}")


if __name__ == "__main__":
    main()
