package checker

import (
	"context"
	"fmt"
	"time"

	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
)

type NodeValue struct {
	Address string
	Value   []byte
	Found   bool
}

type KeyReport struct {
	Key       string
	Consistent bool
	Nodes     []NodeValue
}

type Report struct {
	KeysChecked int
	Consistent  bool
	Keys        []KeyReport
}

type Checker struct {
	Clients []NamedClient
}

type NamedClient struct {
	Address string
	Client  kvpb.KVStoreClient
}

func New(clients []NamedClient) *Checker {
	return &Checker{Clients: clients}
}

// CheckKeys reads the same keys from every node and reports whether values match.
func (c *Checker) CheckKeys(ctx context.Context, keys []string) Report {
	report := Report{Consistent: true, KeysChecked: len(keys)}

	for _, key := range keys {
		kr := KeyReport{Key: key, Consistent: true}
		var ref []byte
		var refFound bool
		var haveRef bool

		for _, nc := range c.Clients {
			resp, err := nc.Client.Get(ctx, &kvpb.GetRequest{Key: key})
			nv := NodeValue{Address: nc.Address}
			if err != nil {
				nv.Found = false
				kr.Consistent = false
				report.Consistent = false
			} else {
				nv.Found = resp.Found
				nv.Value = append([]byte(nil), resp.Value...)
			}
			kr.Nodes = append(kr.Nodes, nv)

			if err != nil {
				continue
			}
			if !haveRef {
				ref = nv.Value
				refFound = nv.Found
				haveRef = true
				continue
			}
			if nv.Found != refFound || !bytesEqual(nv.Value, ref) {
				kr.Consistent = false
				report.Consistent = false
			}
		}
		report.Keys = append(report.Keys, kr)
	}
	return report
}

// WaitForConvergence polls until all nodes return matching values or the timeout expires.
func (c *Checker) WaitForConvergence(ctx context.Context, keys []string, settle time.Duration) (Report, error) {
	deadline := time.Now().Add(settle)
	var last Report

	for {
		last = c.CheckKeys(ctx, keys)
		if last.Consistent {
			return last, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("nodes did not converge within %s", settle)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r Report) String() string {
	status := "CONSISTENT"
	if !r.Consistent {
		status = "INCONSISTENT"
	}
	out := fmt.Sprintf("%s: checked %d keys\n", status, r.KeysChecked)
	for _, key := range r.Keys {
		if key.Consistent {
			continue
		}
		out += fmt.Sprintf("  key %s:\n", key.Key)
		for _, node := range key.Nodes {
			out += fmt.Sprintf("    %s found=%v value=%q\n", node.Address, node.Found, node.Value)
		}
	}
	return out
}
