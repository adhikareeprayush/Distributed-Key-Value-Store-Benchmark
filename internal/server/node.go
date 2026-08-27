package server

import (
	"context"
	"sync"

	"github.com/adhikareeprayush/kv-store/internal/buffer"
	"github.com/adhikareeprayush/kv-store/internal/hlc"
	"github.com/adhikareeprayush/kv-store/internal/replication"
	"github.com/adhikareeprayush/kv-store/internal/store"
	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
	repb "github.com/adhikareeprayush/kv-store/proto/replication"
)

// ReadTracker records keys read by clients so causal Puts can attach dependencies.
type ReadTracker struct {
	mu    sync.Mutex
	reads []store.Dependency
}

func NewReadTracker() *ReadTracker {
	return &ReadTracker{}
}

func (rt *ReadTracker) Record(key string, ts hlc.Timestamp) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.reads = append(rt.reads, store.Dependency{Key: key, MinTS: ts})
}

func (rt *ReadTracker) SnapshotAndClear() []store.Dependency {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := append([]store.Dependency(nil), rt.reads...)
	rt.reads = nil
	return out
}

type Node struct {
	kvpb.UnimplementedKVStoreServer
	repb.UnimplementedReplicationServer

	Mode    string
	Store   *store.Store
	Clock   *hlc.Clock
	Buffer  *buffer.Buffer
	Handler replication.Handler
	Tracker *ReadTracker
}

func NewNode(mode string, s *store.Store, clk *hlc.Clock, buf *buffer.Buffer, handler replication.Handler) *Node {
	n := &Node{
		Mode:    mode,
		Store:   s,
		Clock:   clk,
		Buffer:  buf,
		Handler: handler,
	}
	if mode == "causal" {
		n.Tracker = NewReadTracker()
	}
	return n
}

func (n *Node) Put(ctx context.Context, req *kvpb.PutRequest) (*kvpb.PutResponse, error) {
	ts, err := n.Clock.Send()
	if err != nil {
		return nil, err
	}

	val := store.Value{
		Data:      append([]byte(nil), req.Value...),
		Timestamp: ts,
	}
	if n.Mode == "causal" && n.Tracker != nil {
		val.Deps = n.Tracker.SnapshotAndClear()
	}

	if n.Mode == "strong" {
		if err := n.Handler.OnWrite(ctx, req.Key, val); err != nil {
			return nil, err
		}
		n.Store.Put(req.Key, val)
	} else {
		n.Store.Put(req.Key, val)
		if err := n.Handler.OnWrite(ctx, req.Key, val); err != nil {
			return nil, err
		}
	}

	return &kvpb.PutResponse{Timestamp: toProtoTS(ts)}, nil
}

func (n *Node) Get(ctx context.Context, req *kvpb.GetRequest) (*kvpb.GetResponse, error) {
	val, ok := n.Store.Get(req.Key)
	if !ok {
		return &kvpb.GetResponse{Found: false}, nil
	}

	if n.Mode == "causal" && n.Tracker != nil {
		n.Tracker.Record(req.Key, val.Timestamp)
	}

	return &kvpb.GetResponse{
		Value:     val.Data,
		Timestamp: toProtoTS(val.Timestamp),
		Found:     true,
	}, nil
}

func (n *Node) Delete(ctx context.Context, req *kvpb.DeleteRequest) (*kvpb.DeleteResponse, error) {
	ts, err := n.Clock.Send()
	if err != nil {
		return nil, err
	}

	val := store.Value{Timestamp: ts, Deleted: true}
	if n.Mode == "causal" && n.Tracker != nil {
		val.Deps = n.Tracker.SnapshotAndClear()
	}

	if n.Mode == "strong" {
		if err := n.Handler.OnWrite(ctx, req.Key, val); err != nil {
			return nil, err
		}
		n.Store.Delete(req.Key, ts)
	} else {
		n.Store.Delete(req.Key, ts)
		if err := n.Handler.OnWrite(ctx, req.Key, val); err != nil {
			return nil, err
		}
	}

	return &kvpb.DeleteResponse{Timestamp: toProtoTS(ts)}, nil
}

func (n *Node) Replicate(ctx context.Context, req *repb.ReplicateRequest) (*repb.ReplicateResponse, error) {
	if req.Timestamp == nil {
		return &repb.ReplicateResponse{Success: false}, nil
	}

	inTS := fromProtoTS(req.Timestamp)
	if _, err := n.Clock.Receive(inTS); err != nil {
		return nil, err
	}

	val := store.Value{
		Data:      append([]byte(nil), req.Value...),
		Timestamp: inTS,
		Deps:      fromProtoDeps(req.Deps),
		Deleted:   len(req.Value) == 0,
	}

	if n.Mode == "causal" && n.Buffer != nil {
		n.Buffer.Receive(req.Key, val)
		return &repb.ReplicateResponse{Success: true}, nil
	}

	var ok bool
	if val.Deleted {
		ok = n.Store.ApplyDelete(req.Key, inTS)
	} else {
		ok = n.Store.Apply(req.Key, val)
	}
	return &repb.ReplicateResponse{Success: ok}, nil
}

func toProtoTS(ts hlc.Timestamp) *kvpb.HLCTimestamp {
	return &kvpb.HLCTimestamp{WallTime: ts.WallTime, Logical: ts.Logical}
}

func fromProtoTS(ts *repb.HLCTimestamp) hlc.Timestamp {
	return hlc.Timestamp{WallTime: ts.WallTime, Logical: ts.Logical}
}

func fromProtoDeps(deps []*repb.Dependency) []store.Dependency {
	out := make([]store.Dependency, 0, len(deps))
	for _, dep := range deps {
		if dep == nil || dep.MinTs == nil {
			continue
		}
		out = append(out, store.Dependency{
			Key: dep.Key,
			MinTS: hlc.Timestamp{
				WallTime: dep.MinTs.WallTime,
				Logical:  dep.MinTs.Logical,
			},
		})
	}
	return out
}
