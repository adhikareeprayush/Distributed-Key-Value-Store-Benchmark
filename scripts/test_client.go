//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := "localhost:50051"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := kvpb.NewKVStoreClient(conn)
	key := "e2e-smoke-key"

	if _, err := client.Put(ctx, &kvpb.PutRequest{Key: key, Value: []byte("hello")}); err != nil {
		fmt.Fprintf(os.Stderr, "put: %v\n", err)
		os.Exit(1)
	}

	resp, err := client.Get(ctx, &kvpb.GetRequest{Key: key})
	if err != nil {
		fmt.Fprintf(os.Stderr, "get: %v\n", err)
		os.Exit(1)
	}
	if !resp.Found || string(resp.Value) != "hello" {
		fmt.Fprintf(os.Stderr, "unexpected get: found=%v value=%q\n", resp.Found, resp.Value)
		os.Exit(1)
	}

	if _, err := client.Delete(ctx, &kvpb.DeleteRequest{Key: key}); err != nil {
		fmt.Fprintf(os.Stderr, "delete: %v\n", err)
		os.Exit(1)
	}

	resp, err = client.Get(ctx, &kvpb.GetRequest{Key: key})
	if err != nil {
		fmt.Fprintf(os.Stderr, "get after delete: %v\n", err)
		os.Exit(1)
	}
	if resp.Found {
		fmt.Fprintf(os.Stderr, "expected key to be deleted\n")
		os.Exit(1)
	}
}
