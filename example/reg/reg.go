package main

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/button-chen/drpc"
)

// 参数列表
type pst struct {
	Data string `json:"data"`
	Key  string `json:"key"`
}

// 返回值列表
type rst struct {
	Result string `json:"result"`
}

func myHMACMD5(data, key []byte) string {
	hmac := hmac.New(md5.New, key)
	hmac.Write(data)

	return hex.EncodeToString(hmac.Sum(nil))
}

var myHMACMD5Doc = `
函数名：myHMACMD5 
功能: 计算hmac-md5
参数(json)：
{
	"data":"",    // hmac-md5需要加密的字符串
	"key":""	  // hmac-md5需要的key
}
返回值(json)：
{
	"result":""
}
`

var (
	addr = flag.String("addr", "127.0.0.1:8080", "address")
	name = flag.String("reg", "myHMACMD5", "address")
)

func main() {
	flag.Parse()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// 创建一个rpc客户端实例
	rpcclient := drpc.NewDRPCClient()
	// 连接到服务器
	rpcclient.ConnectToDRPC(*addr)
	// 注册功能函数
	err := rpcclient.Register(*name, myHMACMD5Doc, myHMACMD5, (*pst)(nil), (*rst)(nil))
	if err == nil {
		log.Println("注册成功")
	} else {
		log.Println("注册失败: ", err)
	}

	<-interrupt
}
