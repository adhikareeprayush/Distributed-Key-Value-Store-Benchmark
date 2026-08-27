package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/adhikareeprayush/kv-store/bench"
	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	target := flag.String("target", "localhost:50051", "gRPC target address")
	workloadPath := flag.String("workload", "bench/workloads/workload_a.yaml", "Workload YAML path")
	mode := flag.String("mode", "eventual", "Consistency mode label for CSV output")
	output := flag.String("output", "bench/results/summary.csv", "Summary CSV output path")
	seed := flag.Int64("seed", 0, "RNG seed (0 = random)")
	peerDelayMs := flag.Int("peer-delay-ms", 0, "Label: simulated peer delay on servers (ms)")
	skipLoad := flag.Bool("skip-load", false, "Skip the load phase")
	timeout := flag.Duration("timeout", 5*time.Minute, "Overall benchmark timeout")
	flag.Parse()

	workload, err := bench.LoadWorkload(*workloadPath)
	if err != nil {
		log.Fatalf("load workload: %v", err)
	}

	conn, err := grpc.NewClient(*target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := kvpb.NewKVStoreClient(conn)
	driver := bench.NewDriver(client, workload, *mode, *seed)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if !*skipLoad {
		log.Printf("loading %d records...", workload.RecordCount)
		if err := driver.Load(ctx); err != nil {
			log.Fatalf("load: %v", err)
		}
	}

	log.Printf("running %d operations (mode=%s)...", workload.OperationCount, *mode)
	result, err := driver.Run(ctx)
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	result.PeerDelayMs = *peerDelayMs

	if err := bench.WriteSummaryCSV(*output, result); err != nil {
		log.Fatalf("write csv: %v", err)
	}

	fmt.Printf("Benchmark complete\n")
	fmt.Printf("  mode:       %s\n", result.Mode)
	fmt.Printf("  workload:   %s\n", result.Workload)
	fmt.Printf("  operations: %d (reads=%d writes=%d errors=%d)\n", result.Operations, result.Reads, result.Writes, result.Errors)
	fmt.Printf("  throughput: %.2f ops/s\n", result.Throughput)
	fmt.Printf("  latency p50: %s\n", time.Duration(result.LatencyP50NS))
	fmt.Printf("  results:    %s\n", *output)
}
