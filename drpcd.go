package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DRPC 分布式rpc
type DRPC struct {
	// 所有已经注册的方法名
	MethodOwner map[string]chan DRpcMsg
	mtxm        sync.Mutex

	// WaitResp(mark--timestamp and connnect) 等待应答
	WaitResp map[int64]connWait
	mtxw     sync.Mutex

	// 有注册到来需要通知的集群成员
	NotifyMember []chan DRpcMsg

	// 集群相关
	// 当前集群中的地址  ip:port 用于当自己和集群的连接
	// 断了，顺序找一个可以连接的加入集群
	ClusterAddr map[string]struct{}
	// 监听地址
	addr string

	stop chan struct{}
}

type connWait struct {
	// 创建时间
	createTm time.Time
	// 原始消息
	msg  DRpcMsg
	conn chan DRpcMsg
}

// NewDRPC 新建
func NewDRPC() *DRPC {
	rpc := &DRPC{}
	rpc.init()
	return rpc
}

// Run 启动服务
func (d *DRPC) Run(addr string) {
	d.init()
	http.HandleFunc("/", d.acceptConn)
	http.HandleFunc("/functions", d.debugView)
	go func() {
		log.Fatal(http.ListenAndServe(addr, nil))
	}()
	d.addr = addr
}

// RunPeer 启动服务并且连接到集群
func (d *DRPC) RunPeer(addr, peerAddr string) {
	d.Run(addr)
	d.connectPeer(peerAddr)
	d.ClusterAddr[peerAddr] = struct{}{}
}

// Stop 停止服务
func (d *DRPC) Stop() {
	close(d.stop)
}

func (d *DRPC) init() {
	d.MethodOwner = make(map[string]chan DRpcMsg)
	d.WaitResp = make(map[int64]connWait)
	d.NotifyMember = make([]chan DRpcMsg, 0)
	d.ClusterAddr = make(map[string]struct{})
	d.stop = make(chan struct{})
}

func (d *DRPC) connectPeer(peerAddr string) error {
	u := url.URL{Scheme: "ws", Host: peerAddr, Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Println("dial peer:", err)
		return err
	}
	que := make(chan DRpcMsg, 512)
	done := make(chan struct{})
	go d.readPeer(c, que, done)
	go d.writeMessage(c, que, done)
	return nil
}

func (d *DRPC) readPeer(c *websocket.Conn, que chan DRpcMsg, done chan struct{}) {
	d.registerNotify(que)
	for {
		_, err := d._readMessage(c, que)
		if err != nil {
			close(done)
			break
		}
		select {
		case <-d.stop:
			return
		case <-done:
			return
		default:
		}
	}
	// 找到一个可以连接的兄弟
	for addr := range d.ClusterAddr {
		if addr == d.addr {
			continue
		}
		if d.connectPeer(addr) == nil {
			log.Println("try connect peer success ", addr)
			break
		}
	}
}

// registerNotify 注册功能通知函数, 集群之间使用
func (d *DRPC) registerNotify(que chan DRpcMsg) {
	// 把自己拥有的兄弟网络信息给对方
	var vTmp []string
	for addr := range d.ClusterAddr {
		vTmp = append(vTmp, addr)
	}
	// 自己的监听地址也加进去
	vTmp = append(vTmp, d.addr)

	netAddrInfo, _ := json.Marshal(vTmp)
	msg := DRpcMsg{
		Type: TypeRegNotify,
		Body: string(netAddrInfo),
	}
	que <- msg

	// 更新所有集群的网络地址
	d.updateClusterNetAddrs(string(netAddrInfo))
}

func (d *DRPC) updateClusterNetAddrs (netAddrInfo string) {
	for _, que := range d.NotifyMember {
		msg := DRpcMsg{
			Type: TypeUpdateNetAddr,
			Body: netAddrInfo,
		}
		select {
		case que <- msg:
		case <-time.After(time.Second):
			log.Println("TypeUpdateNetAddr timeout")
		}
		que <- msg
	}
}

var upgrader = websocket.Upgrader{}

func (d *DRPC) acceptConn(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	que := make(chan DRpcMsg, 512)
	done := make(chan struct{})
	go d.readMessage(c, que, done)
	go d.writeMessage(c, que, done)
	go d.timeoutDelete()
}

func (d *DRPC) debugView(w http.ResponseWriter, r *http.Request) {
	nameList := d.getAllRegFnName()
	var netAddrs []string
	for addr := range d.ClusterAddr {
		netAddrs = append(netAddrs, addr)
	}
	dbgInfo := struct {
		Methods []string
		Addrs []string
	}{
		nameList,
		netAddrs,
	}
	info, _ := json.Marshal(dbgInfo)
	w.Write(info)
}

func (d *DRPC) timeoutDelete() {
	ticker := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-ticker.C:
			curTm := time.Now()
			d.mtxw.Lock()
			for id, ct := range d.WaitResp {
				if curTm.Sub(ct.createTm).Milliseconds() < ct.msg.Timeout {
					continue
				}
				delete(d.WaitResp, id)
				log.Println("timeout id: ", id)
				msg := ct.msg
				msg.ErrCode = ErrCodeRepTimeout
				d.writeError(msg, ct.conn)
			}
			d.mtxw.Unlock()

		case <-d.stop:
			ticker.Stop()
			return
		}
	}
}

func (d *DRPC) writeMessage(c *websocket.Conn, que chan DRpcMsg, done chan struct{}) {
	for {
		select {
		case msg := <- que:
			c.WriteJSON(msg)

		case <-done:
			return
		case <-d.stop:
			return
		}
	}
}

func (d *DRPC) readMessage(c *websocket.Conn, que chan DRpcMsg, done chan struct{}) {
	for {
		ty, err := d._readMessage(c, que)
		if err != nil {
			close(done)
			break
		}
		if ty == TypeRegNotify {
			// 收到注册的同时也需要注册到对方
			d.registerNotify(que)
		}
		select {
		case <- done:
			return
		case <-d.stop:
			return
		default:
		}
	}
}

func (d *DRPC) _readMessage(c *websocket.Conn, que chan DRpcMsg) (int, error) {
	mt, message, err := c.ReadMessage()
	if err != nil {
		fnNames := d.del(que)
		for _, fnName := range fnNames {
			d.unRegNotify(fnName)
		}
		return TypeUnknow, err
	}
	// 所有的通讯都是基于json协议
	if mt != websocket.TextMessage {
		return TypeUnknow, nil
	}
	var msg DRpcMsg
	err = json.Unmarshal(message, &msg)
	if err != nil {
		msg.ErrCode = ErrCodeParamFormatError
		d.writeError(msg, que)
		return TypeUnknow, nil
	}
	switch msg.Type {
	case TypeReg:
		log.Println("recv TypeReg ", msg.FuncName)
		d.reg(msg, que)
	case TypeCall:
		d.call(msg, que)
	case TypeResp:
		d.resp(msg)
	case TypeRegNotify:
		log.Println("recv TypeRegNotify")
		d.regNotify(msg, que)
	case TypeUnReg:
		log.Println("recv TypeUnReg")
		d.unReg(msg)
	case TypeUpdateNetAddr:
		log.Println("recv TypeUpdateNetAddr")
		d.update(msg)
	}
	return msg.Type, nil
}

func (d *DRPC) regNotify(msg DRpcMsg, que chan DRpcMsg) {
	d.NotifyMember = append(d.NotifyMember, que)
	// 收到有集群成员注册通知， 一开始就把所有的注册函数给它
	nameList := d.getAllRegFnName()
	for _, name := range nameList {
		m := DRpcMsg{
			Type:     TypeReg,
			FuncName: name,
		}
		que <- m
	}
	// 解析对方给出的集群兄弟的ip:port
	var netAddrs []string
	json.Unmarshal([]byte(msg.Body), &netAddrs)
	for _, addr := range netAddrs {
		d.ClusterAddr[addr] = struct{}{}
	}
}

func (d *DRPC) unRegNotify(fnName string) {
	m := DRpcMsg{
		Type:     TypeUnReg,
		FuncName: fnName,
	}
	for _, que := range d.NotifyMember {
		que <- m
	}
}

func (d *DRPC) getAllRegFnName() []string {
	d.mtxm.Lock()
	defer d.mtxm.Unlock()
	var nameList []string
	for fnName := range d.MethodOwner {
		nameList = append(nameList, fnName)
	}
	return nameList
}

func (d *DRPC) notify(msg DRpcMsg, curQue chan DRpcMsg) {
	for _, que := range d.NotifyMember {
		if que == curQue {
			continue
		}
		select {
		case que <- msg:
		case <-time.After(time.Second):
			log.Println("notify timeout")
		}
	}
}

func (d *DRPC) reg(msg DRpcMsg, que chan DRpcMsg) {
	d.mtxm.Lock()
	defer d.mtxm.Unlock()
	if _, ok := d.MethodOwner[msg.FuncName]; !ok {
		d.MethodOwner[msg.FuncName] = que

		d.notify(msg, que)
		return
	}
	msg.Type = TypeResp
	msg.ErrCode = ErrCodeFunctionBeRegistered
	d.writeError(msg, que)
}

func (d *DRPC) unReg(msg DRpcMsg) {
	d.mtxm.Lock()
	delete(d.MethodOwner, msg.FuncName)
	d.mtxm.Unlock()
}

func (d *DRPC) update(msg DRpcMsg) {
	var netAddrs []string
	json.Unmarshal([]byte(msg.Body), &netAddrs)

	for _, addr := range netAddrs {
		d.ClusterAddr[addr] = struct{}{}
	}
}

func (d *DRPC) call(msg DRpcMsg, que chan DRpcMsg) {
	d.mtxm.Lock()
	otherQue, ok := d.MethodOwner[msg.FuncName]
	d.mtxm.Unlock()
	if !ok {
		msg.ErrCode = ErrCodeFunctionNotExist
		d.writeError(msg, que)
		return
	}
	otherQue <- msg

	d.mtxw.Lock()
	d.WaitResp[msg.UniqueID] = connWait{time.Now(), msg, que}
	d.mtxw.Unlock()
}

func (d *DRPC) del(que chan DRpcMsg) []string {
	fnNames := make([]string, 0)
	d.mtxm.Lock()
	for fnName, conn := range d.MethodOwner {
		if que == conn {
			delete(d.MethodOwner, fnName)
			fnNames = append(fnNames, fnName)
		}
	}
	d.mtxm.Unlock()

	d.mtxw.Lock()
	for id, ct := range d.WaitResp {
		if ct.conn == que {
			delete(d.WaitResp, id)
		}
	}
	d.mtxw.Unlock()

	return fnNames
}

func (d *DRPC) resp(msg DRpcMsg) {
	d.mtxw.Lock()
	defer d.mtxw.Unlock()
	ct, ok := d.WaitResp[msg.UniqueID]
	if !ok {
		return
	}
	delete(d.WaitResp, msg.UniqueID)

	ct.conn <- msg
}

func (d *DRPC) writeError(msg DRpcMsg, que chan DRpcMsg) {
	que <- msg
}
