## drpc功能设计图
https://blog.csdn.net/chen802311/article/details/103534785

## drpc部署
- drpc实现为可以单机或者多机分布式部署
- 单机部署例子：  
drpcd.exe -addr=:8080  
启动了一个监听8080端口的drpcd服务，其他的各种运用程序可以把自己实现的功能注册上去，也可以方便的调用其他运用程序注册的函数功能，实现功能共享。

- 多机部署例子：  
drpcd.exe -addr=192.168.1.11:8080          // 启动第一个drpcd服务  
drpcd.exe -addr=192.168.1.12:8080 -peer=192.168.1.11:8080    // 加入到IP 11机器的drpcd服务群  
drpcd.exe -addr=192.168.1.13:8080 -peer=192.168.1.12:8080    // 加入到IP 12机器的drpcd服务群  
...   
此时这些drpcd服务会相互连接为一个连通无环图， 比如有程序在IP 11机器上注册了函数 foo ，此时另外的程序可以连接IP 13机器调用foo函数(即使foo函数没有被直接注册到IP 13的机器上) 所有被注册的功能函数在drpcd服务群中是共享的。  

- 浏览器访问： ip:port/drpcd/methodsdoc 可以看到当前drpcd群中所有注册的函数与使用说明(由于对html不熟，目前直接返回json给浏览器，格式很丑陋，之后会使用html展示)  
- 浏览器访问： ip:port/drpcd/clusteraddrs 可以看到当前drpcd群中所有drpcd的网络地址
 
注意：  
1：单台电脑上测试多机部署，可以用相同的ip 不同的绑定端口测试  
2：单机部署的addr命令行参数可以只写端口不写IP， 多机部署必须写完整的IP与端口  

## 开发

- go get github.com/button-chen/drpc/...
- golang版本大于等于 1.13.5

## Example
运用程序注册功能到平台:

```golang
package main

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
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
	rpcclient.Register(*name, myHMACMD5Doc, myHMACMD5)

	<-interrupt
}
```

运用程序调用平台中被注册的功能, 方法一:  
```golang
post 方式访问：http://ip:port/drpcd/call   
参数(json)：  
{  
	"FuncName": "foo",    // 函数名  
	"Timeout": 1000,      // 超时毫秒数  
	"Body": ""	      // 约定的json格式参数  
}  
返回值(json)：  
{  
	"ErrCode": 1,     // drpcd返回的错误代码  
	"Body": ""        // 约定的json格式调用返回值  
}  
返回值中错误代码：  
1：成功  2：参数格式错误  3：请求超时  4：函数不存在   
  
注：http方式的客户端只支持调用共享功能，而且只能是同步调用，不支持注册功能。  
``` 

运用程序调用平台中被注册的功能， 方法二:  
  
```golang
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
```
注： 使用专用客户端库，即可以调用共享功能也可以注册共享功能，而且支持同步与异步调用。

