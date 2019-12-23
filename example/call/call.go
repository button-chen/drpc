package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/button-chen/drpc"
)

var (
	addr = flag.String("addr", "127.0.0.1:8080", "address")
	name = flag.String("call", "myHMACMD5", "function name")
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
	// 同步调用(不适合高频率的调用方式)
	param := struct {
		Key  string `json:"key"`
		Data string `json:"data"`
	}{
		"123",
		"mydata",
	}
	t, _ := json.Marshal(param)
	ret, err := rpcclient.Call(*name, t, 2000)

	tmp := struct {
		Result string `json:"result"`
	}{}
	if err != nil {
		log.Println("err:", err.Error())
	} else {
		json.Unmarshal(ret, &tmp)
		log.Println("result:", tmp.Result)
	}

	// 异步调用的回调函数，效率较高可以适用高频率调用
	fn := func(id int64, msg []byte, err error) {
		if err != nil {
			log.Println("err: ", err.Error())
		} else {
			json.Unmarshal(ret, &tmp)
			log.Println("return async: ", tmp.Result)
		}
	}
	for {
		id := rpcclient.AsyncCall(*name, t, 2000, fn)
		// 返回的id与回调结果的第一个参数一致
		_ = id
		time.Sleep(time.Millisecond * 10)
	}

	<-interrupt
}
