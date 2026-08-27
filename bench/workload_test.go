package bench

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWorkloadA(t *testing.T) {
	w, err := LoadWorkload("workloads/workload_a.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "workload_a" {
		t.Fatalf("unexpected name: %s", w.Name)
	}
	if w.RecordCount != 1000 || w.OperationCount != 10000 {
		t.Fatalf("unexpected counts: %+v", w)
	}
}

func TestSummarizeLatencies(t *testing.T) {
	p50, p95, p99, avg := summarize([]int64{10, 20, 30, 40, 100})
	if p50 != 30 || p95 != 40 || p99 != 40 || avg != 40 {
		t.Fatalf("unexpected percentiles: p50=%d p95=%d p99=%d avg=%d", p50, p95, p99, avg)
	}
}

func TestWriteSummaryCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.csv")

	res := Result{
		Mode:         "eventual",
		Workload:     "workload_a",
		Seed:         42,
		Operations:   10,
		Reads:        5,
		Writes:       5,
		Throughput:   100.5,
		LatencyP50NS: 1000,
	}
	if err := WriteSummaryCSV(path, res); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected csv output")
	}
}

func TestZipfianSkew(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	const n = 1000
	const samples = 100000
	countLow := 0
	countHigh := 0
	for i := 0; i < samples; i++ {
		idx := zipfianIndex(r, n, 0.99)
		if idx < 10 {
			countLow++
		}
		if idx >= n-10 {
			countHigh++
		}
	}
	if countLow <= countHigh {
		t.Fatalf("expected low-index keys to dominate under zipfian skew: low=%d high=%d", countLow, countHigh)
	}
}

func TestScrambledZipfianUsesFullRange(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	seen := make(map[int]struct{})
	for i := 0; i < 10000; i++ {
		seen[scrambledZipfianIndex(r, 1000, 0.99)] = struct{}{}
	}
	if len(seen) < 200 {
		t.Fatalf("scrambled zipfian should spread across keyspace, got %d unique keys", len(seen))
	}
}

func TestLoadWorkloadWriteHeavy(t *testing.T) {
	w, err := LoadWorkload("workloads/workload_write_heavy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if w.ReadProportion != 0.1 || w.UpdateProportion != 0.9 {
		t.Fatalf("unexpected mix: %+v", w)
	}
}
