package replication

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/adhikareeprayush/kv-store/internal/buffer"
	"github.com/adhikareeprayush/kv-store/internal/hlc"
	"github.com/adhikareeprayush/kv-store/internal/store"
	pb "github.com/adhikareeprayush/kv-store/proto/replication"
)

// Options configures optional replication behavior (used by experiments).
type Options struct {
	DelayPrefix string        // delay keys matching this prefix (causal-chain experiments)
	Delay       time.Duration // extra delay for DelayPrefix keys
	PeerDelay   time.Duration // simulated network latency on every peer Replicate RPC
}

func sleepBeforeReplicate(opts Options, key string) {
	var d time.Duration
	if opts.PeerDelay > 0 {
		d += opts.PeerDelay
	}
	if opts.Delay > 0 && opts.DelayPrefix != "" && strings.HasPrefix(key, opts.DelayPrefix) {
		d += opts.Delay
	}
	if d > 0 {
		time.Sleep(d)
	}
}

// Handler is called on the originating node after a local Put succeeds.
// main.go selects one implementation at startup from --mode; no runtime switching.
type Handler interface {
	OnWrite(ctx context.Context, key string, val store.Value) error
}

// EventualHandler replicates writes asynchronously with no causal dependencies.
type EventualHandler struct {
	Peers []pb.ReplicationClient
	Store *store.Store
	HLC   *hlc.Clock
	Opts  Options
}

func (h *EventualHandler) OnWrite(ctx context.Context, key string, val store.Value) error {
	req := buildReplicateRequest(key, val, nil)
	for _, peer := range h.Peers {
		peer := peer
		opts := h.Opts
		go func() {
			sleepBeforeReplicate(opts, key)
			_, _ = peer.Replicate(context.Background(), req)
		}()
	}
	return nil
}

// CausalHandler replicates writes with dependency metadata; peers apply via buffer.
type CausalHandler struct {
	Peers  []pb.ReplicationClient
	Store  *store.Store
	HLC    *hlc.Clock
	Buffer *buffer.Buffer
	Opts   Options
}

func (h *CausalHandler) OnWrite(ctx context.Context, key string, val store.Value) error {
	req := buildReplicateRequest(key, val, val.Deps)
	for _, peer := range h.Peers {
		peer := peer
		opts := h.Opts
		go func() {
			sleepBeforeReplicate(opts, key)
			_, _ = peer.Replicate(context.Background(), req)
		}()
	}
	return nil
}

// StrongHandler blocks until a write quorum is acknowledged.
type StrongHandler struct {
	Peers  []pb.ReplicationClient
	Store  *store.Store
	HLC    *hlc.Clock
	Quorum int // floor(n/2) + 1; local node counts as 1
	Opts   Options
}

func (h *StrongHandler) OnWrite(ctx context.Context, key string, val store.Value) error {
	if len(h.Peers) == 0 {
		return nil
	}

	req := buildReplicateRequest(key, val, nil)
	remoteNeeded := h.Quorum - 1
	if remoteNeeded <= 0 {
		return nil
	}

	type result struct {
		ok bool
	}
	results := make(chan result, len(h.Peers))

	var wg sync.WaitGroup
	for _, peer := range h.Peers {
		wg.Add(1)
		go func(p pb.ReplicationClient) {
			defer wg.Done()
			sleepBeforeReplicate(h.Opts, key)
			resp, err := p.Replicate(ctx, req)
			results <- result{ok: err == nil && resp != nil && resp.Success}
		}(peer)
	}
	wg.Wait()
	close(results)

	acks := 0
	for r := range results {
		if r.ok {
			acks++
		}
	}
	if acks < remoteNeeded {
		return errors.New("replication: quorum not reached")
	}
	return nil
}

func buildReplicateRequest(key string, val store.Value, deps []store.Dependency) *pb.ReplicateRequest {
	req := &pb.ReplicateRequest{
		Key:   key,
		Value: append([]byte(nil), val.Data...),
		Timestamp: &pb.HLCTimestamp{
			WallTime: val.Timestamp.WallTime,
			Logical:  val.Timestamp.Logical,
		},
	}
	for _, dep := range deps {
		req.Deps = append(req.Deps, &pb.Dependency{
			Key: dep.Key,
			MinTs: &pb.HLCTimestamp{
				WallTime: dep.MinTS.WallTime,
				Logical:  dep.MinTS.Logical,
			},
		})
	}
	return req
}

func NewHandler(mode string, peers []pb.ReplicationClient, s *store.Store, clk *hlc.Clock, buf *buffer.Buffer, opts Options) Handler {
	switch mode {
	case "eventual":
		return &EventualHandler{Peers: peers, Store: s, HLC: clk, Opts: opts}
	case "causal":
		return &CausalHandler{Peers: peers, Store: s, HLC: clk, Buffer: buf, Opts: opts}
	case "strong":
		total := len(peers) + 1
		return &StrongHandler{Peers: peers, Store: s, HLC: clk, Quorum: total/2 + 1, Opts: opts}
	default:
		panic("invalid mode") // main.go validates before calling
	}
}
