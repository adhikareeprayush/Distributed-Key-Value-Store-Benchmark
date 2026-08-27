package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/adhikareeprayush/kv-store/internal/checker"
	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	nodes := flag.String("nodes", "localhost:50051,localhost:50052,localhost:50053", "Comma-separated node addresses")
	keyPrefix := flag.String("key-prefix", "user", "Key prefix to check")
	keyCount := flag.Int("key-count", 100, "Number of keys to check")
	settle := flag.Duration("settle", 3*time.Second, "How long to wait for replication convergence")
	timeout := flag.Duration("timeout", 30*time.Second, "Overall timeout")
	flag.Parse()

	nodeList := parseList(*nodes)
	if len(nodeList) == 0 {
		log.Fatal("no nodes provided")
	}

	clients := make([]checker.NamedClient, 0, len(nodeList))
	conns := make([]*grpc.ClientConn, 0, len(nodeList))
	for _, addr := range nodeList {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatalf("dial %s: %v", addr, err)
		}
		conns = append(conns, conn)
		clients = append(clients, checker.NamedClient{
			Address: addr,
			Client:  kvpb.NewKVStoreClient(conn),
		})
	}
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()

	keys := make([]string, *keyCount)
	for i := 0; i < *keyCount; i++ {
		keys[i] = fmt.Sprintf("%s%d", *keyPrefix, i)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	chk := checker.New(clients)
	report, err := chk.WaitForConvergence(ctx, keys, *settle)
	fmt.Print(report.String())
	if err != nil {
		log.Fatalf("check failed: %v", err)
	}
	if !report.Consistent {
		os.Exit(1)
	}
}

func parseList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
