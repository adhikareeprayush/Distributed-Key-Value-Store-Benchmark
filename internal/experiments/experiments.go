package experiments

import (
	"context"
	"fmt"
	"time"

	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	Addr   string
	Client kvpb.KVStoreClient
	conn   *grpc.ClientConn
}

func Dial(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{
		Addr:   addr,
		Client: kvpb.NewKVStoreClient(conn),
		conn:   conn,
	}, nil
}

func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type StalenessResult struct {
	Trials    int
	Visible   int
	MeanMs    float64
	P95Ms     float64
	Timeouts  int
	SamplesMs []float64
}

// MeasureStaleness writes on origin and polls replica until the value is visible.
func MeasureStaleness(ctx context.Context, origin, replica *Client, trials int) (StalenessResult, error) {
	res := StalenessResult{Trials: trials}
	samples := make([]float64, 0, trials)

	for i := 0; i < trials; i++ {
		key := fmt.Sprintf("staleness-%d", i)
		val := []byte(fmt.Sprintf("v-%d-%d", i, time.Now().UnixNano()))

		if _, err := origin.Client.Put(ctx, &kvpb.PutRequest{Key: key, Value: val}); err != nil {
			return res, fmt.Errorf("put: %w", err)
		}

		deadline := time.Now().Add(5 * time.Second)
		visible := false
		start := time.Now()
		for time.Now().Before(deadline) {
			resp, err := replica.Client.Get(ctx, &kvpb.GetRequest{Key: key})
			if err != nil {
				return res, err
			}
			if resp.Found && string(resp.Value) == string(val) {
				elapsed := float64(time.Since(start).Milliseconds())
				samples = append(samples, elapsed)
				visible = true
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if visible {
			res.Visible++
		} else {
			res.Timeouts++
		}
	}

	res.SamplesMs = samples
	if len(samples) > 0 {
		res.MeanMs = mean(samples)
		res.P95Ms = percentile(samples, 0.95)
	}
	return res, nil
}

type CausalChainResult struct {
	Trials          int
	Violations      int
	TrialViolations []bool
}

// MeasureCausalChains runs read-then-write chains and checks for ordering violations on a replica.
// Pair with --replicate-delay-prefix=chain-a- on the origin node so B can arrive before A on replicas
// in eventual mode while causal mode buffers B until A is visible.
func MeasureCausalChains(ctx context.Context, origin, replica *Client, trials int) (CausalChainResult, error) {
	res := CausalChainResult{
		Trials:          trials,
		TrialViolations: make([]bool, 0, trials),
	}

	for i := 0; i < trials; i++ {
		keyA := fmt.Sprintf("chain-a-%d", i)
		keyB := fmt.Sprintf("chain-b-%d", i)
		valA := []byte(fmt.Sprintf("a-%d", i))
		valB := []byte(fmt.Sprintf("b-%d", i))

		if _, err := origin.Client.Put(ctx, &kvpb.PutRequest{Key: keyA, Value: valA}); err != nil {
			return res, err
		}
		if _, err := origin.Client.Get(ctx, &kvpb.GetRequest{Key: keyA}); err != nil {
			return res, err
		}
		if _, err := origin.Client.Put(ctx, &kvpb.PutRequest{Key: keyB, Value: valB}); err != nil {
			return res, err
		}

		violated := pollCausalViolation(ctx, replica, keyA, valA, keyB, valB, 500*time.Millisecond)
		res.TrialViolations = append(res.TrialViolations, violated)
		if violated {
			res.Violations++
		}
	}
	return res, nil
}

func pollCausalViolation(ctx context.Context, replica *Client, keyA string, valA []byte, keyB string, valB []byte, window time.Duration) bool {
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		respB, err := replica.Client.Get(ctx, &kvpb.GetRequest{Key: keyB})
		if err != nil {
			return false
		}
		respA, err := replica.Client.Get(ctx, &kvpb.GetRequest{Key: keyA})
		if err != nil {
			return false
		}

		if respB.Found && string(respB.Value) == string(valB) {
			if !respA.Found || string(respA.Value) != string(valA) {
				return true
			}
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]float64(nil), vals...)
	for i := 0; i < len(cp); i++ {
		for j := i + 1; j < len(cp); j++ {
			if cp[j] < cp[i] {
				cp[i], cp[j] = cp[j], cp[i]
			}
		}
	}
	idx := int(float64(len(cp)-1) * p)
	return cp[idx]
}
