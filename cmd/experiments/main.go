package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/adhikareeprayush/kv-store/internal/experiments"
)

func main() {
	test := flag.String("test", "all", "Test: staleness | causal | all")
	origin := flag.String("origin", "localhost:50051", "Origin node address")
	replica := flag.String("replica", "localhost:50052", "Replica node address")
	mode := flag.String("mode", "eventual", "Mode label for output")
	trials := flag.Int("trials", 30, "Number of trials")
	output := flag.String("output", "bench/results/paper/experiments.csv", "CSV output path")
	flag.Parse()

	originClient, err := experiments.Dial(*origin)
	if err != nil {
		log.Fatal(err)
	}
	defer originClient.Close()

	replicaClient, err := experiments.Dial(*replica)
	if err != nil {
		log.Fatal(err)
	}
	defer replicaClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	switch *test {
	case "staleness":
		runStaleness(ctx, *mode, originClient, replicaClient, *trials, *output)
	case "causal":
		runCausal(ctx, *mode, originClient, replicaClient, *trials, *output)
	case "all":
		runStaleness(ctx, *mode, originClient, replicaClient, *trials, *output)
		runCausal(ctx, *mode, originClient, replicaClient, *trials, *output)
	default:
		log.Fatalf("unknown test %q", *test)
	}
}

func runStaleness(ctx context.Context, mode string, origin, replica *experiments.Client, trials int, output string) {
	res, err := experiments.MeasureStaleness(ctx, origin, replica, trials)
	if err != nil {
		log.Fatalf("staleness: %v", err)
	}

	for i, ms := range res.SamplesMs {
		appendTrialRow(output, []string{
			mode, "staleness", fmt.Sprintf("%d", i),
			fmt.Sprintf("%.2f", ms), "",
		})
	}
	for i := len(res.SamplesMs); i < trials; i++ {
		appendTrialRow(output, []string{
			mode, "staleness", fmt.Sprintf("%d", i),
			"", "timeout",
		})
	}

	fmt.Printf("staleness [%s]: visible=%d/%d mean=%.1fms p95=%.1fms timeouts=%d\n",
		mode, res.Visible, trials, res.MeanMs, res.P95Ms, res.Timeouts)
}

func runCausal(ctx context.Context, mode string, origin, replica *experiments.Client, trials int, output string) {
	res, err := experiments.MeasureCausalChains(ctx, origin, replica, trials)
	if err != nil {
		log.Fatalf("causal: %v", err)
	}

	for i, violated := range res.TrialViolations {
		val := "0"
		if violated {
			val = "1"
		}
		appendTrialRow(output, []string{
			mode, "causal_chain", fmt.Sprintf("%d", i),
			"", val,
		})
	}

	rate := 0.0
	if trials > 0 {
		rate = float64(res.Violations) / float64(trials) * 100
	}
	fmt.Printf("causal_chain [%s]: violations=%d/%d (%.1f%%)\n",
		mode, res.Violations, trials, rate)
}

func appendTrialRow(path string, row []string) {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		log.Fatal(err)
	}
	exists := fileExists(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if !exists {
		_ = w.Write([]string{"mode", "test", "trial", "staleness_ms", "violation"})
	}
	_ = w.Write(row)
	w.Flush()
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
