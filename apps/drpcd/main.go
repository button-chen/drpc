package main

import (
	"flag"
	"github.com/button-chen/drpc"
	"log"
	"os"
	"os/signal"
)

var (
	addr     = flag.String("addr", ":8080", "address")
	peerAddr = flag.String("peer", "", "address")
)

func main() {
	flag.Parse()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	rpc := drpc.NewDRPC()

	if len(*peerAddr) != 0 {
		log.Println("add peer: ", *addr, *peerAddr)
		rpc.RunPeer(*addr, *peerAddr)
	} else {
		log.Println("run ", *addr)
		rpc.Run(*addr)
	}

	select {
	case <-interrupt:
		rpc.Stop()
	}
}
