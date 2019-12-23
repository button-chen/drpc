package main

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/button-chen/drpc"
)

// 被注册的函数参数与返回值都必须为[]byte，实际上就是json格式
// 思想： 把任何需要注册的函数的参数和返回值都json化进行调用
func myHMACMD5(param []byte) []byte {
	pts := struct {
		Key  string `json:"key"`
		Data string `json:"data"`
	}{}
	err := json.Unmarshal(param, &pts)
	if err != nil {
		return []byte("")
	}
	hmac := hmac.New(md5.New, []byte(pts.Key))
	hmac.Write([]byte(pts.Data))

	ret := struct {
		Result string `json:"result"`
	}{
		hex.EncodeToString(hmac.Sum(nil)),
	}
	t, _ := json.Marshal(ret)
	return t
}

var myHMACMD5Doc = `
函数名：myHMACMD5 
功能: 计算hmac-md5
参数(json)：
{
	"key":"",    // hmac-md5需要的key
	"data":""    // hmac-md5需要加密的字符串
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
	err := rpcclient.Register(*name, myHMACMD5Doc, myHMACMD5)
	if err == nil {
		log.Println("注册成功")
	} else {
		log.Println("注册失败: ", err)
	}

	<-interrupt
}
