# drpc特点
- drpc实现为可以单机或者多机分布式部署
- 单机部署例子：  
drpcd.exe -addr=:8080  
启动了一个监听8080端口的drpcd服务，其他的各种运用程序可以把自己实现的功能注册上去，也可以方便的调用其他运用程序注册的函数功能，实现功能共享

- 多机部署例子：  
drpcd.exe -addr=192.168.1.11:8080          // 启动第一个drpcd服务  
drpcd.exe -addr=192.168.1.12:8080 -peer=192.168.1.11:8080    // 加入到11机器所在的drpcd服务群  
drpcd.exe -addr=192.168.1.13:8080 -peer=192.168.1.12:8080    // 加入到12机器所在的drpcd服务群  
...   
此时这些drpcd服务会相互连接为一个连通无环图， 比如有程序在IP 11上注册了函数 foo ，那么另外的程序可以通过IP 13调用foo函数， 所有被注册的功能函数实现跨机器共享  
  
注意：  
1：单机上测试多机部署， 可以用相同的ip 不同的绑定端口测试  
2：单机部署的addr命令行参数可以只写端口不写IP， 多机部署必须写完整的IP与端口  


# 使用

- go get github.com/button-chen/drpc/...
- golang版本大于等于 1.13.5

## Example
运用程序注册功能到平台:

```golang
package main

import (
	"github.com/button-chen/drpc"
	"os"
	"os/signal"
)

// 被注册的函数参数与返回值都必须为[]byte，实际上就是json格式
// 思想： 把任何需要注册的函数的参数和返回值都json化进行调用，虽
// 然看上去有点别扭，但是这种实现确实是最简单的
func foo(param []byte) []byte {
	return []byte("call foo")
}

func main() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// 创建一个rpc客户端实例
	rpcclient := drpc.NewDRPCClient()
	// 连接到服务器
	rpcclient.ConnectToDRPC("127.0.0.1:8080")
	// 注册功能函数
	rpcclient.Register("foo", foo)

	<-interrupt
}
```

运用程序调用平台中被注册的功能:

```golang
package main

import (
	"log"
	"time"

	"github.com/button-chen/drpc"
)

func main() {

	rpcclient := drpc.NewDRPCClient()
	rpcclient.ConnectToDRPC("127.0.0.1:8080")

	// 同步调用(不适合高频率的调用方式)
	ret, err := rpcclient.Call("test", []byte("myparam"), 2000)
	if err != nil {
		log.Println("err:", err.Error())
	} else {
		log.Println("result:", string(ret))
	}

	// 异步调用的回调函数，效率较高可以适用高频率调用
	fn := func(id int64, msg []byte, err error) {
		if err != nil {
			log.Println("err: ", err.Error())
		} else {
			log.Println("return: ", id, string(msg))
		}
	}
	for {
		id := rpcclient.AsyncCall("foo", []byte("myparam"), 2000, fn)
		// 返回的id与回调结果的第一个参数一致
		_ = id
		time.Sleep(time.Millisecond * 10)
	}
}
```
