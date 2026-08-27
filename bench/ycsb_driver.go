package bench

import (
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"

	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
)

// Result is one benchmark summary row matching docs/benchmark_schema.md.
type Result struct {
	Mode           string
	Workload       string
	Seed           int64
	PeerDelayMs    int
	Operations     int
	Reads          int
	Writes         int
	Errors         int
	Duration       time.Duration
	Throughput     float64
	LatencyP50NS   int64
	LatencyP95NS   int64
	LatencyP99NS   int64
	LatencyAvgNS   int64
}

type Driver struct {
	Client   kvpb.KVStoreClient
	Workload Workload
	Mode     string
	Seed     int64
	Rand     *rand.Rand
}

func NewDriver(client kvpb.KVStoreClient, w Workload, mode string, seed int64) *Driver {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &Driver{
		Client:   client,
		Workload: w,
		Mode:     mode,
		Seed:     seed,
		Rand:     rand.New(rand.NewSource(seed)),
	}
}

func (d *Driver) Load(ctx context.Context) error {
	value := make([]byte, d.Workload.FieldLength)
	for i := range value {
		value[i] = byte('a' + (i % 26))
	}

	for i := 0; i < d.Workload.RecordCount; i++ {
		key := d.Workload.KeyFor(i)
		if _, err := d.Client.Put(ctx, &kvpb.PutRequest{Key: key, Value: append([]byte(nil), value...)}); err != nil {
			return fmt.Errorf("load %s: %w", key, err)
		}
	}
	return nil
}

func (d *Driver) Run(ctx context.Context) (Result, error) {
	latencies := make([]int64, 0, d.Workload.OperationCount)
	res := Result{
		Mode:     d.Mode,
		Workload: d.Workload.Name,
		Seed:     d.Seed,
	}

	start := time.Now()
	for i := 0; i < d.Workload.OperationCount; i++ {
		op := d.pickOperation()
		key := d.pickKey()

		opStart := time.Now()
		var err error
		switch op {
		case "read":
			_, err = d.Client.Get(ctx, &kvpb.GetRequest{Key: key})
			res.Reads++
		case "update", "insert":
			value := make([]byte, d.Workload.FieldLength)
			for j := range value {
				value[j] = byte('x' + ((i + j) % 26))
			}
			_, err = d.Client.Put(ctx, &kvpb.PutRequest{Key: key, Value: value})
			res.Writes++
		default:
			err = fmt.Errorf("unknown operation %q", op)
		}

		latencies = append(latencies, time.Since(opStart).Nanoseconds())
		res.Operations++
		if err != nil {
			res.Errors++
		}
	}
	res.Duration = time.Since(start)
	res.Throughput = float64(res.Operations) / res.Duration.Seconds()
	res.LatencyP50NS, res.LatencyP95NS, res.LatencyP99NS, res.LatencyAvgNS = summarize(latencies)
	return res, nil
}

func (d *Driver) pickOperation() string {
	r := d.Rand.Float64()
	if r < d.Workload.ReadProportion {
		return "read"
	}
	r -= d.Workload.ReadProportion
	if r < d.Workload.UpdateProportion {
		return "update"
	}
	return "insert"
}

func (d *Driver) pickKey() string {
	n := d.Workload.RecordCount
	var idx int
	switch d.Workload.RequestDistribution {
	case "zipfian":
		idx = scrambledZipfianIndex(d.Rand, n, d.Workload.ZipfianConstant)
	case "uniform":
		idx = d.Rand.Intn(n)
	default:
		idx = d.Rand.Intn(n)
	}
	return d.Workload.KeyFor(idx)
}

// scrambledZipfianIndex implements YCSB CoreWorkload's ScrambledZipfianGenerator:
// sample ZipfianGenerator then permute with FNV-1a 64-bit hash (see YCSB utils/scrambledzipfian.go).
func scrambledZipfianIndex(r *rand.Rand, n int, theta float64) int {
	if n <= 1 {
		return 0
	}
	raw := zipfianIndex(r, n, theta)
	return int(fnvHash64(int64(raw)) % uint64(n))
}

// zipfianIndex implements YCSB ZipfianGenerator: min + int(n * u^(1/theta)).
func zipfianIndex(r *rand.Rand, n int, theta float64) int {
	if n <= 1 {
		return 0
	}
	if theta <= 0 {
		theta = 0.99
	}
	u := r.Float64()
	idx := int(float64(n) * math.Pow(u, 1.0/theta))
	if idx >= n {
		idx = n - 1
	}
	return idx
}

func fnvHash64(val int64) uint64 {
	const (
		offset64 = 0xcbf29ce484222325
		prime64  = 0x100000001b3
	)
	hash := uint64(offset64)
	for i := 0; i < 8; i++ {
		hash ^= uint64(val & 0xff)
		hash *= prime64
		val >>= 8
	}
	return hash
}

func summarize(latencies []int64) (p50, p95, p99, avg int64) {
	if len(latencies) == 0 {
		return 0, 0, 0, 0
	}
	sorted := append([]int64(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum int64
	for _, v := range sorted {
		sum += v
	}
	avg = sum / int64(len(sorted))
	p50 = percentile(sorted, 0.50)
	p95 = percentile(sorted, 0.95)
	p99 = percentile(sorted, 0.99)
	return p50, p95, p99, avg
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func WriteSummaryCSV(path string, res Result) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}

	exists := fileExists(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if !exists {
		if err := w.Write(SummaryHeader()); err != nil {
			return err
		}
	}
	if err := w.Write(res.Row()); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func SummaryHeader() []string {
	return []string{
		"mode", "workload", "seed", "peer_delay_ms", "operations", "reads", "writes", "errors",
		"duration_ms", "throughput_ops", "latency_p50_ns", "latency_p95_ns",
		"latency_p99_ns", "latency_avg_ns",
	}
}

func (r Result) Row() []string {
	return []string{
		r.Mode,
		r.Workload,
		fmt.Sprintf("%d", r.Seed),
		fmt.Sprintf("%d", r.PeerDelayMs),
		fmt.Sprintf("%d", r.Operations),
		fmt.Sprintf("%d", r.Reads),
		fmt.Sprintf("%d", r.Writes),
		fmt.Sprintf("%d", r.Errors),
		fmt.Sprintf("%.3f", r.Duration.Seconds()*1000),
		fmt.Sprintf("%.2f", r.Throughput),
		fmt.Sprintf("%d", r.LatencyP50NS),
		fmt.Sprintf("%d", r.LatencyP95NS),
		fmt.Sprintf("%d", r.LatencyP99NS),
		fmt.Sprintf("%d", r.LatencyAvgNS),
	}
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
