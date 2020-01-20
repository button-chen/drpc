package main

import (
	"flag"
	"time"

	"github.com/button-chen/drpc"
)

var (
	addr = flag.String("addr", "127.0.0.1:8080", "address")
	name = flag.String("pub", "mytopic", "topic name")
)

func main() {
	flag.Parse()

	rpcclient := drpc.NewDRPCClient()
	rpcclient.ConnectToDRPC(*addr)

	ticker := time.NewTicker(time.Millisecond * time.Duration(200))
	for t := range ticker.C {
		rpcclient.Pub(*name, t.String(), 500)
	}
}
