package drpc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DRpcClient 客户端类
type DRpcClient struct {
	conn *websocket.Conn
	// 注册函数
	regFunc map[string]func(param []byte) []byte
	// 接收call结果
	result map[int64]chan DRpcMsg
	// result 的同步锁
	mu sync.Mutex

	// 异步使用
	asyncCache chan func()
	aresult    map[int64]func(int64, []byte, error)
	amu        sync.Mutex

	// 发布订阅
	topicCallback map[string]func(string, error)

	stop chan struct{}
}

// NewDRPCClient 新建
func NewDRPCClient() *DRpcClient {
	return &DRpcClient{
		regFunc:       make(map[string]func(param []byte) []byte),
		result:        make(map[int64]chan DRpcMsg),
		asyncCache:    make(chan func(), 1024),
		aresult:       make(map[int64]func(int64, []byte, error)),
		topicCallback: make(map[string]func(string, error)),
		stop:          make(chan struct{}),
	}
}

// ConnectToDRPC 连接到服务器
func (d *DRpcClient) ConnectToDRPC(addr string) error {
	u := url.URL{Scheme: "ws", Host: addr, Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}
	d.conn = c
	go d.read()
	go d.execCallback()
	return nil
}

// Call 同步调用
func (d *DRpcClient) Call(fnName string, param []byte, timeout int64) ([]byte, error) {
	uid := d.createUniqueID()
	req := DRpcMsg{
		Type:     TypeCall,
		UniqueID: uid,
		FuncName: fnName,
		Timeout:  timeout,
		Body:     string(param),
	}
	return d.call(req)
}

// AsyncCall 异步调用
func (d *DRpcClient) AsyncCall(fnName string, param []byte, timeout int64, fn func(int64, []byte, error)) int64 {
	uid := d.createUniqueID()
	req := DRpcMsg{
		Type:     TypeCall,
		UniqueID: uid,
		FuncName: fnName,
		Timeout:  timeout,
		Body:     string(param),
	}
	d.amu.Lock()
	d.aresult[uid] = fn
	d.amu.Unlock()

	d.conn.WriteJSON(req)
	return uid
}

// Register 注册功能函数
func (d *DRpcClient) Register(fnName, callDoc string, fn func(param []byte) []byte) error {
	d.regFunc[fnName] = fn

	uid := d.createUniqueID()
	req := DRpcMsg{
		Type:     TypeReg,
		FuncName: fnName,
		Doc:      callDoc,
		UniqueID: uid,
	}
	_, err := d.call(req)
	return err
}

// Sub 订阅
func (d *DRpcClient) Sub(topic string, fn func(string, error)) {
	d.topicCallback[topic] = fn
	param := DRpcMsg{
		Type:     TypeSub,
		FuncName: topic,
	}
	err := d.conn.WriteJSON(param)
	if err != nil {
		log.Println("Sub:", err)
		return
	}
}

// Pub 发布
func (d *DRpcClient) Pub(topic, content string, timeout int64) {
	param := DRpcMsg{
		Type:     TypePub,
		FuncName: topic,
		Body:     content,
		Timeout:  timeout,
	}
	err := d.conn.WriteJSON(param)
	if err != nil {
		log.Println("Pub:", err)
		return
	}
}

func (d *DRpcClient) call(msg DRpcMsg) ([]byte, error) {
	retChan := make(chan DRpcMsg, 1)
	d.mu.Lock()
	d.result[msg.UniqueID] = retChan
	d.mu.Unlock()

	d.conn.WriteJSON(msg)

	if msg.Timeout == 0 {
		msg.Timeout = 1000
	}

	var ret DRpcMsg
	var err error
	select {
	case ret = <-retChan:
	case <-time.After(time.Millisecond * time.Duration(msg.Timeout*2)):
		ret.ErrCode = ErrCodeRepTimeout
		log.Println("timeout ms: ", msg.Timeout)
	case <-d.stop:
		return []byte(ret.Body), nil
	}
	if ret.ErrCode != ErrCodeOK {
		err = fmt.Errorf("errCode: %d", ret.ErrCode)
	}
	return []byte(ret.Body), err
}

func (d *DRpcClient) createUniqueID() int64 {
	return time.Now().UnixNano()
}

func (d *DRpcClient) execCallback() {
	for fn := range d.asyncCache {
		fn()
	}
}

func (d *DRpcClient) recvSub(msg DRpcMsg) {
	fn, ok := d.topicCallback[msg.FuncName]
	if !ok {
		log.Println("not topic: ", msg.FuncName)
		return
	}
	fn(msg.Body, nil)
}

func (d *DRpcClient) recvCall(msg DRpcMsg) {
	fn, ok := d.regFunc[msg.FuncName]
	if !ok {
		log.Println("not function: ", msg.FuncName)
		return
	}
	ret := fn([]byte(msg.Body))
	msg.Body = string(ret)
	msg.Type = TypeResp
	msg.ErrCode = ErrCodeOK
	err := d.conn.WriteJSON(msg)
	if err != nil {
		log.Println("resp:", err)
		return
	}
}

func (d *DRpcClient) recvResp(msg DRpcMsg) {
	d.mu.Lock()
	retChan, ok := d.result[msg.UniqueID]
	d.mu.Unlock()
	if ok {
		retChan <- msg
		close(retChan)

		d.mu.Lock()
		delete(d.result, msg.UniqueID)
		d.mu.Unlock()
		return
	}
	d.amu.Lock()
	fn, ok := d.aresult[msg.UniqueID]
	d.amu.Unlock()
	if ok {
		newFn := func() {
			var err error
			m := msg
			if m.ErrCode != ErrCodeOK {
				err = fmt.Errorf("errCode async: %d", m.ErrCode)
			}
			fn(m.UniqueID, []byte(m.Body), err)
		}
		d.asyncCache <- newFn
		return
	}
	log.Println("not exist unique id")
}

func (d *DRpcClient) read() {
	for {
		_, message, err := d.conn.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			close(d.asyncCache)
			return
		}
		var msg DRpcMsg
		err = json.Unmarshal(message, &msg)
		if err != nil {
			continue
		}
		switch msg.Type {
		case TypeCall:
			d.recvCall(msg)
		case TypeResp:
			d.recvResp(msg)
		case TypeSub:
			d.recvSub(msg)
		default:
			log.Println("unknow type: ", msg)
		}
	}
}
