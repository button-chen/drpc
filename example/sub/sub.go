package main

import (
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/button-chen/drpc"
)

var (
	addr = flag.String("addr", "127.0.0.1:8080", "address")
	name = flag.String("sub", "mytopic", "topic name")
)

func main() {
	flag.Parse()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	rpcclient := drpc.NewDRPCClient()
	err := rpcclient.ConnectToDRPC(*addr)
	if err != nil {
		log.Println(err.Error())
		return
	}

	fn := func(msg string, err error) {
		log.Println("recv topic data: ", msg)
	}
	rpcclient.Sub(*name, fn)

	<-interrupt
}
