//go:build ignore

package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	addr := "localhost:50051"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		os.Exit(1)
	}
	_ = conn.Close()
	fmt.Println("ready")
}
