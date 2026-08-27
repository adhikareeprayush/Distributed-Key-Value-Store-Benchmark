package replication

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adhikareeprayush/kv-store/internal/hlc"
	"github.com/adhikareeprayush/kv-store/internal/store"
	pb "github.com/adhikareeprayush/kv-store/proto/replication"
	"google.golang.org/grpc"
)

type mockReplicationClient struct {
	pb.ReplicationClient
	mu    sync.Mutex
	calls int
	last  *pb.ReplicateRequest
	done  chan struct{}
}

func (m *mockReplicationClient) Replicate(ctx context.Context, in *pb.ReplicateRequest, opts ...grpc.CallOption) (*pb.ReplicateResponse, error) {
	m.mu.Lock()
	m.calls++
	m.last = in
	if m.done != nil {
		close(m.done)
		m.done = nil
	}
	m.mu.Unlock()
	return &pb.ReplicateResponse{Success: true}, nil
}

func TestNewHandlerModes(t *testing.T) {
	s := store.New()
	clk := hlc.New()

	for _, mode := range []string{"eventual", "causal", "strong"} {
		h := NewHandler(mode, nil, s, clk, nil, Options{})
		if h == nil {
			t.Fatalf("nil handler for mode %s", mode)
		}
	}
}

func TestEventualHandlerReplicatesAsync(t *testing.T) {
	done := make(chan struct{})
	mock := &mockReplicationClient{done: done}
	h := &EventualHandler{Peers: []pb.ReplicationClient{mock}}

	val := store.Value{
		Data:      []byte("v"),
		Timestamp: hlc.Timestamp{WallTime: 1, Logical: 0},
	}
	if err := h.OnWrite(context.Background(), "k", val); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async replicate")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.calls != 1 {
		t.Fatalf("expected 1 replicate call, got %d", mock.calls)
	}
	if mock.last.GetKey() != "k" {
		t.Fatalf("unexpected key: %s", mock.last.GetKey())
	}
}

func TestStrongHandlerPeerDelay(t *testing.T) {
	start := time.Now()
	h := &StrongHandler{
		Peers:  []pb.ReplicationClient{&mockReplicationClient{}},
		Quorum: 2,
		Opts:   Options{PeerDelay: 50 * time.Millisecond},
	}
	val := store.Value{Data: []byte("v"), Timestamp: hlc.Timestamp{WallTime: 1}}
	if err := h.OnWrite(context.Background(), "k", val); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 45*time.Millisecond {
		t.Fatalf("expected peer delay to block quorum path, got %v", elapsed)
	}
}
func TestStrongHandlerQuorum(t *testing.T) {
	ok := &mockReplicationClient{}
	h := &StrongHandler{
		Peers:  []pb.ReplicationClient{&failingClient{}, &failingClient{}},
		Quorum: 2,
	}

	val := store.Value{Data: []byte("v"), Timestamp: hlc.Timestamp{WallTime: 1}}
	if err := h.OnWrite(context.Background(), "k", val); err == nil {
		t.Fatal("expected quorum failure with zero acks")
	}

	h2 := &StrongHandler{
		Peers:  []pb.ReplicationClient{ok, &mockReplicationClient{}},
		Quorum: 2,
	}
	if err := h2.OnWrite(context.Background(), "k", val); err != nil {
		t.Fatalf("expected quorum success: %v", err)
	}
}

type failingClient struct{}

func (f *failingClient) Replicate(ctx context.Context, in *pb.ReplicateRequest, opts ...grpc.CallOption) (*pb.ReplicateResponse, error) {
	return &pb.ReplicateResponse{Success: false}, nil
}
