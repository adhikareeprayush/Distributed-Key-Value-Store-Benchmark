package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/adhikareeprayush/kv-store/internal/buffer"
	"github.com/adhikareeprayush/kv-store/internal/hlc"
	"github.com/adhikareeprayush/kv-store/internal/replication"
	"github.com/adhikareeprayush/kv-store/internal/server"
	"github.com/adhikareeprayush/kv-store/internal/store"
	kvpb "github.com/adhikareeprayush/kv-store/proto/kv"
	repb "github.com/adhikareeprayush/kv-store/proto/replication"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	mode := flag.String("mode", "eventual", "Consistency mode: eventual | causal | strong")
	port := flag.Int("port", 50051, "gRPC listen port")
	peers := flag.String("peers", "", "Comma-separated peer addresses")
	delayPrefix := flag.String("replicate-delay-prefix", "", "Key prefix to delay before replicating (experiments)")
	delay := flag.Duration("replicate-delay", 0, "Delay before replicating keys matching --replicate-delay-prefix")
	peerDelay := flag.Duration("peer-delay", 0, "Simulated network latency added before each peer Replicate RPC")
	pprofAddr := flag.String("pprof", "", "If set, serve Go pprof on this address (e.g. localhost:6060)")

	flag.Parse()

	modeVal := *mode
	portVal := *port
	peersVal := *peers

	validModes := []string{"eventual", "causal", "strong"}
	if !contains(validModes, modeVal) {
		log.Fatalf("invalid --mode %q: must be one of %v", modeVal, validModes)
	}

	peerList := parsePeers(peersVal)

	s := store.New()
	clk := hlc.New()
	var buf *buffer.Buffer
	if modeVal == "causal" {
		buf = buffer.New(s)
	}

	peerClients, conns := dialPeers(peerList)
	defer closeConns(conns)

	handler := replication.NewHandler(modeVal, peerClients, s, clk, buf, replication.Options{
		DelayPrefix: *delayPrefix,
		Delay:       *delay,
		PeerDelay:   *peerDelay,
	})
	node := server.NewNode(modeVal, s, clk, buf, handler)

	if *pprofAddr != "" {
		go func() {
			log.Printf("pprof: http://%s/debug/pprof/", *pprofAddr)
			if err := http.ListenAndServe(*pprofAddr, nil); err != nil {
				log.Printf("pprof server: %v", err)
			}
		}()
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", portVal))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	kvpb.RegisterKVStoreServer(grpcServer, node)
	repb.RegisterReplicationServer(grpcServer, node)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("serve: %v", err)
		}
	}()

	fmt.Printf("Node starting...\n")
	fmt.Printf("Mode: %s\n", modeVal)
	fmt.Printf("Port: %d\n", portVal)
	fmt.Printf("Peers: %v\n", peerList)
	if *peerDelay > 0 {
		fmt.Printf("Peer delay: %s\n", *peerDelay)
	}
	if *pprofAddr != "" {
		fmt.Printf("Pprof: %s\n", *pprofAddr)
	}
	fmt.Println("Node running. Press Ctrl+C to stop.")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("Shutting down.")
	grpcServer.GracefulStop()
}

func dialPeers(peerList []string) ([]repb.ReplicationClient, []*grpc.ClientConn) {
	clients := make([]repb.ReplicationClient, 0, len(peerList))
	conns := make([]*grpc.ClientConn, 0, len(peerList))

	for _, addr := range peerList {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Printf("warning: failed to dial peer %s: %v", addr, err)
			continue
		}
		conns = append(conns, conn)
		clients = append(clients, repb.NewReplicationClient(conn))
	}
	return clients, conns
}

func closeConns(conns []*grpc.ClientConn) {
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func parsePeers(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
