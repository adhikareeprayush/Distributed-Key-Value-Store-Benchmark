package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/adhikareeprayush/kv-store/internal/buffer"
	"github.com/adhikareeprayush/kv-store/internal/experiments"
	"github.com/adhikareeprayush/kv-store/internal/hlc"
	"github.com/adhikareeprayush/kv-store/internal/replication"
	"github.com/adhikareeprayush/kv-store/internal/store"
	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
	repb "github.com/adhikareeprayush/kv-store/proto/replication"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func startTestNode(t *testing.T, mode string, lis *bufconn.Listener, peerClients []repb.ReplicationClient, opts replication.Options) *grpc.Server {
	t.Helper()

	s := store.New()
	clk := hlc.New()
	var buf *buffer.Buffer
	if mode == "causal" {
		buf = buffer.New(s)
	}
	handler := replication.NewHandler(mode, peerClients, s, clk, buf, opts)
	node := NewNode(mode, s, clk, buf, handler)

	srv := grpc.NewServer()
	kvpb.RegisterKVStoreServer(srv, node)
	repb.RegisterReplicationServer(srv, node)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	return srv
}

func TestEventualReplicationBetweenTwoNodes(t *testing.T) {
	const bufSize = 1024 * 1024

	lis1 := bufconn.Listen(bufSize)
	lis2 := bufconn.Listen(bufSize)

	dial := func(lis *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return conn
	}

	conn1 := dial(lis1)
	conn2 := dial(lis2)
	t.Cleanup(func() {
		_ = conn1.Close()
		_ = conn2.Close()
	})

	peer2 := repb.NewReplicationClient(conn2)
	peer1 := repb.NewReplicationClient(conn1)

	srv1 := startTestNode(t, "eventual", lis1, []repb.ReplicationClient{peer2}, replication.Options{})
	srv2 := startTestNode(t, "eventual", lis2, []repb.ReplicationClient{peer1}, replication.Options{})
	t.Cleanup(func() {
		srv1.Stop()
		srv2.Stop()
	})

	kv1 := kvpb.NewKVStoreClient(conn1)
	kv2 := kvpb.NewKVStoreClient(conn2)

	ctx := context.Background()
	if _, err := kv1.Put(ctx, &kvpb.PutRequest{Key: "k", Value: []byte("hello")}); err != nil {
		t.Fatalf("put: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := kv2.Get(ctx, &kvpb.GetRequest{Key: "k"})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if resp.Found && string(resp.Value) == "hello" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("replicated value not observed on peer within timeout")
}

func testCausalChain(t *testing.T, mode string, delay time.Duration, wantViolations bool) {
	t.Helper()
	const bufSize = 1024 * 1024

	lis1 := bufconn.Listen(bufSize)
	lis2 := bufconn.Listen(bufSize)

	dial := func(lis *bufconn.Listener) *grpc.ClientConn {
		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return conn
	}

	conn1 := dial(lis1)
	conn2 := dial(lis2)
	t.Cleanup(func() {
		_ = conn1.Close()
		_ = conn2.Close()
	})

	peer2 := repb.NewReplicationClient(conn2)
	peer1 := repb.NewReplicationClient(conn1)

	opts := replication.Options{DelayPrefix: "chain-a-", Delay: delay}
	srv1 := startTestNode(t, mode, lis1, []repb.ReplicationClient{peer2}, opts)
	srv2 := startTestNode(t, mode, lis2, []repb.ReplicationClient{peer1}, replication.Options{})
	t.Cleanup(func() {
		srv1.Stop()
		srv2.Stop()
	})

	origin := &experiments.Client{Client: kvpb.NewKVStoreClient(conn1)}
	replica := &experiments.Client{Client: kvpb.NewKVStoreClient(conn2)}

	res, err := experiments.MeasureCausalChains(context.Background(), origin, replica, 5)
	if err != nil {
		t.Fatalf("MeasureCausalChains: %v", err)
	}
	if wantViolations && res.Violations == 0 {
		t.Fatalf("expected violations in %s mode, got 0/5", mode)
	}
	if !wantViolations && res.Violations > 0 {
		t.Fatalf("expected no violations in %s mode, got %d/5", mode, res.Violations)
	}
}

func TestCausalChainViolationsEventual(t *testing.T) {
	testCausalChain(t, "eventual", 100*time.Millisecond, true)
}

func TestCausalChainNoViolationsCausal(t *testing.T) {
	testCausalChain(t, "causal", 100*time.Millisecond, false)
}
