package drpc

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ConnMsgQue 网络连接消息队列
type ConnMsgQue = chan DRpcMsg

// DRPC 分布式rpc
type DRPC struct {

	// MethodOwner 注册提供者集合
	MethodOwner *Provider

	// WaitResp 等待应答集合
	WaitResp *WaitGroup

	// 发布订阅
	TopicSub *Subscribe

	// 有注册到来需要通知的集群成员
	NotifyMember map[ConnMsgQue]struct{}
	mu sync.RWMutex

	// 集群相关
	// 当前集群中的地址  ip:port 用于当自己和集群的连接
	// 断了，顺序找一个可以连接的加入集群
	ClusterAddr sync.Map
	// 监听地址
	addr string

	// AOP相关
	dbgLog Logger
	handlers Middleware
	monitorFunc MonitorFuncChain

	stop chan struct{}
}

// NewDRPC 新建
func NewDRPC() *DRPC {
	rpc := &DRPC{}
	rpc.init()
	return rpc
}

// Run 启动服务
func (d *DRPC) Run(addr string) {
	http.HandleFunc("/", d.acceptConn)
	http.HandleFunc("/drpcd/methodsdoc", d.drpcdMethodsDoc)
	http.HandleFunc("/drpcd/clusteraddrs", d.drpcdClusterAddrs)
	http.HandleFunc("/drpcd/clustertopics", d.drpcdClusterTopics)

	http.HandleFunc("/drpcd/call", d.httpCall)
	http.HandleFunc("/drpcd/pub", d.httpPub)
	if d.handlers != nil {
		hds := d.handlers.GetMapHandler()
		for path, handler := range hds{
			http.HandleFunc(path, d.createHandler(handler))
		}
	}
	go func() {
		d.dbgLog.Fatal(http.ListenAndServe(addr, nil))
	}()
	d.addr = addr
	go d.deleteTimeout()
}

// RunPeer 启动服务并且连接到集群
func (d *DRPC) RunPeer(addr, peerAddr string) {
	d.Run(addr)
	d.connectPeer(peerAddr)
	d.ClusterAddr.Store(peerAddr, nil)
}

// SetLogger 设置自定义log
func (d *DRPC) SetLogger(logger Logger) {
	d.dbgLog = logger
}

// HTTPUse 使用http作为客户端时，提供接口使兼容
func (d *DRPC) HTTPUse(handlers Middleware) {
	d.handlers = handlers
}

// Use 收到的消息都会回调注册的过程
func (d *DRPC) Use(handlers ...MonitorFunc) {
	d.monitorFunc = append(d.monitorFunc, handlers...)
}

// Stop 停止服务
func (d *DRPC) Stop() {
	close(d.stop)
}

func (d *DRPC) init() {
	d.MethodOwner = NewProvider()
	d.WaitResp = NewWaitGroup(runtime.NumCPU())
	d.TopicSub = NewSubscribe()

	d.NotifyMember = make(map[ConnMsgQue]struct{})
	d.monitorFunc = make(MonitorFuncChain, 0)
	d.dbgLog = &DefLogger{}

	d.stop = make(chan struct{})
}

func (d *DRPC) createHandler(fn HandlerFunc) func(w http.ResponseWriter, r *http.Request) {

	return func (w http.ResponseWriter, r *http.Request){
		fn(w, r, func(msg DRpcMsg) []byte{
			// 构造请求参数
			msg.Type = TypeCall
			msg.UniqueID = time.Now().UnixNano()
			if msg.Timeout == 0 {
				// 超时没有赋值，默认给5秒
				msg.Timeout = 5000
			}
			que := make(ConnMsgQue, 1)
			d.call(msg, que)
			// 等待结果
			select {
			case msg = <-que:
			case <-time.After(time.Millisecond * time.Duration(msg.Timeout)):
				msg.ErrCode = ErrCodeRepTimeout
				msg.Body = "请求超时"
				d.dbgLog.Info("http call timeout: ", msg.Timeout)
			}
			return d.httpError(msg.ErrCode, msg.Body)
		})
	}
}

func (d *DRPC) connectPeer(peerAddr string) error {
	u := url.URL{Scheme: "ws", Host: peerAddr, Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		d.dbgLog.Info("dial peer:", err)
		return err
	}
	que := make(ConnMsgQue, 1024)
	go d.readPeer(c, que)
	go d.writeMessage(c, que)
	return nil
}

// registerNotify 注册功能通知函数, 集群之间使用
func (d *DRPC) registerNotify(que ConnMsgQue) {
	// 把自己拥有的兄弟网络信息给对方
	var vTmp []string
	d.ClusterAddr.Range(func(key interface{}, value interface{}) bool {
		addr := key.(string)
		vTmp = append(vTmp, addr)
		return true
	})
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

func (d *DRPC) updateClusterNetAddrs(netAddrInfo string) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for que := range d.NotifyMember {
		msg := DRpcMsg{
			Type: TypeUpdateNetAddr,
			Body: netAddrInfo,
		}
		select {
		case que <- msg:
		case <-time.After(time.Second):
			d.dbgLog.Info("TypeUpdateNetAddr timeout")
		}
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (d *DRPC) acceptConn(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	que := make(ConnMsgQue, 1024)
	go d.readMessage(c, que)
	go d.writeMessage(c, que)
}

func (d *DRPC) drpcdClusterAddrs(w http.ResponseWriter, r *http.Request) {
	var netAddrs []string
	d.ClusterAddr.Range(func(key interface{}, value interface{}) bool {
		addr := key.(string)
		netAddrs = append(netAddrs, addr)
		return true
	})
	if len(netAddrs) == 0 {
		netAddrs = append(netAddrs, d.addr)
	}
	info, _ := json.Marshal(netAddrs)
	w.Write(info)
}

func (d *DRPC) drpcdClusterTopics(w http.ResponseWriter, r *http.Request) {
	w.Write(d.TopicSub.Info())
}

func (d *DRPC) drpcdMethodsDoc(w http.ResponseWriter, r *http.Request) {
	w.Write(d.MethodOwner.Docs())
}

func (d *DRPC) httpError(errCode int, errMsg string) []byte {

	errReps := struct {
		ErrCode int
		ErrMsg  string
	}{
		errCode,
		errMsg,
	}
	msg, _ := json.Marshal(errReps)
	return msg
}

func (d *DRPC) httpCall(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.Write(d.httpError(ErrCodeUnknow, err.Error()))
		return
	}
	defer r.Body.Close()

	var msg DRpcMsg
	err = json.Unmarshal(body, &msg)
	if err != nil {
		w.Write(d.httpError(ErrCodeParamFormatError, err.Error()))
		return
	}
	// 构造请求参数
	msg.Type = TypeCall
	msg.UniqueID = time.Now().UnixNano()
	if msg.Timeout == 0 {
		// 超时没有赋值，默认给5秒
		msg.Timeout = 5000
	}
	que := make(ConnMsgQue, 1)
	d.call(msg, que)
	// 等待结果
	select {
	case msg = <-que:
	case <-time.After(time.Millisecond * time.Duration(msg.Timeout)):
		msg.ErrCode = ErrCodeRepTimeout
		msg.Body = "请求超时"
		d.dbgLog.Info("http call timeout: ", msg.Timeout)
	}

	w.Write(d.httpError(msg.ErrCode, msg.Body))
}

func (d *DRPC) httpPub(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.Write(d.httpError(ErrCodeUnknow, err.Error()))
		return
	}
	defer r.Body.Close()

	var msg DRpcMsg
	err = json.Unmarshal(body, &msg)
	if err != nil {
		w.Write(d.httpError(ErrCodeParamFormatError, err.Error()))
		return
	}
	msg.Type = TypePub
	d.pub(msg, r.RemoteAddr)

	w.Write(d.httpError(ErrCodeOK, ""))
}

func (d *DRPC) deleteTimeout() {

	duration := time.Millisecond * 500
	timer := time.NewTimer(duration)
	for {
		select {
		case <-timer.C:
			d.WaitResp.ClearExpired()
			timer.Reset(duration)

		case <-d.stop:
			return
		}
	}
}

func (d *DRPC) writeMessage(c *websocket.Conn, que ConnMsgQue) {
	for {
		select {
		case msg, ok := <-que:
			err := c.WriteJSON(msg)
			if err != nil || !ok {
				return
			}
		case <-d.stop:
			return
		}
	}
}

func (d *DRPC) delNotifyMember(que ConnMsgQue) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.NotifyMember, que)
	close(que)
}

func (d *DRPC) getNotifyCount() int{
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.NotifyMember)
}

func (d *DRPC) readPeer(c *websocket.Conn, que ConnMsgQue) {

	types := make(map[int]struct{})
	d.registerNotify(que)
	for {
		ty, err := d._readMessage(c, que)
		types[ty] = struct{}{}
		if err != nil {
			if _, ok := types[TypeSub]; ok {
				d.TopicSub.Del("", c.RemoteAddr().String())
				msg := DRpcMsg{Type:TypeUnSub}
				d.notify(msg, que)
			}
			d.delNotifyMember(que)
			break
		}
		select {
		case <-d.stop:
			return
		default:
		}
	}
	// 如果此时和集群还有关系就不需要去主动连接
	if d.getNotifyCount() > 0 {
		d.dbgLog.Info("exist valid connect from other member")
		return
	}
	// 找到一个可以连接的兄弟
	d.ClusterAddr.Range(func(key interface{}, value interface{}) bool {
		addr := key.(string)
		if addr == d.addr {
			return true
		}
		// 这里有时机问题，当 A -> B   B->A  同时连上，就形成了环， 怎么搞？
		if d.getNotifyCount() > 0 {
			d.dbgLog.Info("new valid connect from other member, not try connect")
			return false
		}
		if d.connectPeer(addr) != nil {
			return true
		}
		d.dbgLog.Info("try connect peer success ", addr)
		return false
	})
}

func (d *DRPC) readMessage(c *websocket.Conn, que ConnMsgQue) {

	types := make(map[int]bool)
	for {
		ty, err := d._readMessage(c, que)
		if _, ok := types[ty]; !ok {
			types[ty] = false
		}
		if err != nil {
			if _, ok := types[TypeSub]; ok {
				d.TopicSub.Del("", c.RemoteAddr().String())
				msg := DRpcMsg{Type:TypeUnSub}
				d.notify(msg, que)
			}
			if _, ok := types[TypeReg]; ok {
				fnNames := d.MethodOwner.DelByQue(que)
				for _, fnName := range fnNames {
					msg := DRpcMsg{
						Type:     TypeUnReg,
						FuncName: fnName,
					}
					d.dbgLog.Info("disconnect TypeUnReg", fnName)
					d.notify(msg, que)
				}
			}
			d.delNotifyMember(que)
			break
		}
		// 收到注册的同时也需要注册到对方
		switch ty {
		case TypeRegNotify:
			if types[ty] {
				continue
			}
			types[ty] = true
			d.registerNotify(que)
		}
		select {
		case <-d.stop:
			return
		default:
		}
	}
}

func (d *DRPC) _readMessage(c *websocket.Conn, que ConnMsgQue) (int, error) {
	mt, message, err := c.ReadMessage()
	if err != nil {
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
	for _, handler := range d.monitorFunc {
		handler(msg)
	}
	switch msg.Type {
	case TypeReg:
		d.dbgLog.Info("recv TypeReg ", msg.FuncName)
		d.reg(msg, que)
	case TypeCall:
		d.call(msg, que)
	case TypeResp:
		d.resp(msg)
	case TypeRegNotify:
		d.dbgLog.Info("recv TypeRegNotify")
		d.regNotify(msg, que)
	case TypeUnReg:
		d.dbgLog.Info("recv TypeUnReg", msg.FuncName)
		d.unReg(msg, que)
	case TypeUpdateNetAddr:
		d.dbgLog.Info("recv TypeUpdateNetAddr")
		d.update(msg)
	case TypeSub:
		d.dbgLog.Info("recv TypeSub")
		d.sub(c.RemoteAddr().String(), msg, que)
	case TypeUnSub:
		d.dbgLog.Info("recv TypeUnSub")
		d.unSub(c.RemoteAddr().String(), msg, que)
	case TypePub:
		d.pub(msg, c.RemoteAddr().String())
	}
	return msg.Type, nil
}

func (d *DRPC) regNotify(msg DRpcMsg, que ConnMsgQue) {
	d.mu.Lock()
	d.NotifyMember[que] = struct{}{}
	d.mu.Unlock()
	// 收到有集群成员注册通知， 一开始就把所有的注册函数给它
	ms, docs := d.MethodOwner.Methods()
	for i, name := range ms {
		m := DRpcMsg{
			Type:     TypeReg,
			FuncName: name,
			Doc:docs[i],
		}
		que <- m
	}
	topicList := d.TopicSub.Topics()
	for _, name := range topicList {
		m := DRpcMsg{
			Type:     TypeSub,
			FuncName: name,
		}
		que <- m
	}
	// 解析对方给出的集群兄弟的ip:port
	var netAddrs []string
	json.Unmarshal([]byte(msg.Body), &netAddrs)
	for _, addr := range netAddrs {
		d.ClusterAddr.Store(addr, nil)
	}
}

func (d *DRPC) notify(msg DRpcMsg, curQue ConnMsgQue) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for que := range d.NotifyMember {
		if que == curQue {
			continue
		}
		select {
		case que <- msg:
		case <-time.After(time.Second):
			d.dbgLog.Info("notify timeout")
		}
	}
}

func (d *DRPC) reg(msg DRpcMsg, que ConnMsgQue) {

	if d.MethodOwner.Add(msg.FuncName, msg.Doc, que) {
		d.notify(msg, que)
		msg.ErrCode = ErrCodeOK
		d.writeError(msg, que)
		return
	}
	msg.ErrCode = ErrCodeFunctionBeRegistered
	d.writeError(msg, que)
}

func (d *DRPC) unReg(msg DRpcMsg, que ConnMsgQue) {
	d.MethodOwner.Del(msg.FuncName)
	d.notify(msg, que)
}

func (d *DRPC) update(msg DRpcMsg) {
	var netAddrs []string
	json.Unmarshal([]byte(msg.Body), &netAddrs)

	for _, addr := range netAddrs {
		d.ClusterAddr.Store(addr, nil)
	}
}

func (d *DRPC) call(msg DRpcMsg, que ConnMsgQue) {

	if !d.MethodOwner.Call(msg) {
		msg.ErrCode = ErrCodeFunctionNotExist
		d.writeError(msg, que)
		return
	}
	d.WaitResp.Get(msg.UniqueID).Add(msg.UniqueID, WaitVal{time.Now(), msg, que})
}

func (d *DRPC) resp(msg DRpcMsg) {
	ct, ok := d.WaitResp.Get(msg.UniqueID).GetAndDel(msg.UniqueID)
	if !ok {
		return
	}
	msg.Type = TypeResp
	ct.conn <- msg
}

func (d *DRPC) del(que ConnMsgQue) []string {
	return d.MethodOwner.DelByQue(que)
}

func (d *DRPC) sub(subAddr string, msg DRpcMsg, que ConnMsgQue) {
	d.TopicSub.Sub(msg.FuncName, subAddr, que)
	d.notify(msg, que)
}

func (d *DRPC) unSub(subAddr string, msg DRpcMsg, que ConnMsgQue) {
	d.TopicSub.Del(msg.FuncName, subAddr)
	d.notify(msg, que)
}

func (d *DRPC) pub(msg DRpcMsg, addr string) {

	d.TopicSub.Pub(msg, addr)
}

func (d *DRPC) writeError(msg DRpcMsg, que ConnMsgQue) {
	msg.Type = TypeResp
	que <- msg
}
