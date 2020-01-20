package drpc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"
	"errors"

	"github.com/gorilla/websocket"
)

const (
	StateConnecting = iota + 1
	StateConnected
	StateNotConnect
)

var (
	ErrNotConnect = errors.New("与服务器断开")
	ErrInternal = errors.New("服务器内部错误")
)

// DRpcClient 客户端类
type DRpcClient struct {
	address string
	conn *websocket.Conn
	// 注册函数
	regFunc *JsonCall
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

	// 网络当前连接状态
	netstate int
	// 注册过的方法，用于断开之后重连时重新注册
	regFuncState map[string]string

	// 网络发送容器
	sendPool chan DRpcMsg

	stop chan struct{}
}

// NewDRPCClient 新建
func NewDRPCClient() *DRpcClient {
	return &DRpcClient{
		regFunc:       &JsonCall{},
		result:        make(map[int64]chan DRpcMsg),
		asyncCache:    make(chan func(), 1024),
		aresult:       make(map[int64]func(int64, []byte, error)),
		topicCallback: make(map[string]func(string, error)),
		regFuncState:  make(map[string]string),
		sendPool: 	   make(chan DRpcMsg, 256),
		stop:          make(chan struct{}),
	}
}

// ConnectToDRPC 连接到服务器
func (d *DRpcClient) ConnectToDRPC(addr string) {
	d.address = addr
	go d.execCallback()

	d.mustConnect(d.address)
}

// Call 同步调用
func (d *DRpcClient) Call(fnName string, param []byte, timeout int64) ([]byte, error) {
	if d.netstate != StateConnected {
		return  nil, ErrNotConnect
	}
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
func (d *DRpcClient) AsyncCall(fnName string, param []byte, timeout int64, fn func(int64, []byte, error)) (int64, error) {
	if d.netstate != StateConnected {
		return 0, ErrNotConnect
	}

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

	err := d.conn.WriteJSON(req)
	if err != nil {
		return 0, ErrInternal
	}
	return uid, nil
}

// Register 注册功能函数
func (d *DRpcClient) Register(fnName, callDoc string, fn, pst, rst interface{}) error {
	d.regFunc.Reg(fnName, fn, pst, rst)
	d.regFuncState[fnName] = callDoc

	if d.netstate != StateConnected {
		return ErrNotConnect
	}

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
	msg := DRpcMsg{
		Type:     TypeSub,
		FuncName: topic,
	}
	if d.netstate != StateConnected {
		return
	}
	d.sendPool <- msg
}

// Pub 发布
func (d *DRpcClient) Pub(topic, content string, timeout int64) error{

	if d.netstate != StateConnected {
		return ErrNotConnect
	}
	msg := DRpcMsg{
		Type:     TypePub,
		FuncName: topic,
		Body:     content,
		Timeout:  timeout,
	}
	d.sendPool <- msg
	return nil
}

func (d *DRpcClient) connect(addr string) error {
	u := url.URL{Scheme: "ws", Host: addr, Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}
	d.conn = c
	go d.read()
	go d.write()
	return nil
}

func (d *DRpcClient) mustConnect(addr string) {
	go func(){
		d.netstate = StateConnecting
		for  {
			err := d.connect(d.address)
			if err != nil {
				time.Sleep(time.Second*2)
				continue
			}
			d.netstate = StateConnected
			d.reRegister()
			d.reSub()
			break
		}
	}()
}

func (d *DRpcClient) reRegister() {
	for fnName, callDoc := range d.regFuncState {
		uid := d.createUniqueID()
		req := DRpcMsg{
			Type:     TypeReg,
			FuncName: fnName,
			Doc:      callDoc,
			UniqueID: uid,
		}
		d.call(req)
	}
}

func (d *DRpcClient) reSub() {
	for topic := range d.topicCallback {
		msg := DRpcMsg{
			Type:     TypeSub,
			FuncName: topic,
		}
		d.sendPool <- msg
	}
}

func (d *DRpcClient) call(msg DRpcMsg) ([]byte, error) {
	retChan := make(chan DRpcMsg, 1)
	d.mu.Lock()
	d.result[msg.UniqueID] = retChan
	d.mu.Unlock()

	d.sendPool <- msg

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
	go func(){
		ret, err := d.regFunc.Call(msg.FuncName, []byte(msg.Body))
		if err != nil {
			log.Println("jsoncall:", err)
		}
		msg.Body = string(ret)
		msg.Type = TypeResp
		msg.ErrCode = ErrCodeOK

		d.sendPool <- msg
	}()
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

	defer d.mustConnect(d.address)
	for {
		_, message, err := d.conn.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			d.netstate = StateNotConnect
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
		case TypePub:
			d.recvSub(msg)
		default:
			log.Println("unknow type: ", msg)
		}
	}
}

func (d *DRpcClient) write() {
	for msg := range d.sendPool {
		if d.netstate != StateConnected {
			d.sendPool <- msg
			continue
		}
		err := d.conn.WriteJSON(msg)
		if err != nil {
			log.Println("写协程: ", err)
		}
	}
}
