package checker

import (
	"context"
	"testing"

	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
	"google.golang.org/grpc"
)

type fakeKVClient struct {
	kvpb.KVStoreClient
	values map[string]*kvpb.GetResponse
}

func (f *fakeKVClient) Get(ctx context.Context, in *kvpb.GetRequest, _ ...grpc.CallOption) (*kvpb.GetResponse, error) {
	resp, ok := f.values[in.Key]
	if !ok {
		return &kvpb.GetResponse{Found: false}, nil
	}
	return resp, nil
}

func TestCheckKeysConsistent(t *testing.T) {
	resp := &kvpb.GetResponse{Found: true, Value: []byte("v")}
	c := New([]NamedClient{
		{Address: "n1", Client: &fakeKVClient{values: map[string]*kvpb.GetResponse{"k": resp}}},
		{Address: "n2", Client: &fakeKVClient{values: map[string]*kvpb.GetResponse{"k": resp}}},
	})

	report := c.CheckKeys(context.Background(), []string{"k"})
	if !report.Consistent {
		t.Fatalf("expected consistent report: %+v", report)
	}
}

func TestCheckKeysInconsistent(t *testing.T) {
	c := New([]NamedClient{
		{Address: "n1", Client: &fakeKVClient{values: map[string]*kvpb.GetResponse{
			"k": {Found: true, Value: []byte("a")},
		}}},
		{Address: "n2", Client: &fakeKVClient{values: map[string]*kvpb.GetResponse{
			"k": {Found: true, Value: []byte("b")},
		}}},
	})

	report := c.CheckKeys(context.Background(), []string{"k"})
	if report.Consistent {
		t.Fatal("expected inconsistent report")
	}
}
