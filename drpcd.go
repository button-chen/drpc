package drpc

import (
	"encoding/json"
	"io/ioutil"
	"log"
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
	MethodOwner *Provider
	// 发布订阅
	TopicSub map[string]map[ConnMsgQue]string
	mtxt     sync.Mutex

	// WaitResp 正在等待应答
	WaitResp *WaitGroup

	// 有注册到来需要通知的集群成员
	NotifyMember []ConnMsgQue

	// 集群相关
	// 当前集群中的地址  ip:port 用于当自己和集群的连接
	// 断了，顺序找一个可以连接的加入集群
	ClusterAddr map[string]struct{}
	// 监听地址
	addr string

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
	d.MethodOwner = NewProvider()
	d.WaitResp = NewWaitGroup(runtime.NumCPU())

	d.NotifyMember = make([]ConnMsgQue, 0)
	d.ClusterAddr = make(map[string]struct{})
	d.TopicSub = make(map[string]map[ConnMsgQue]string)
	d.stop = make(chan struct{})
}

func (d *DRPC) connectPeer(peerAddr string) error {
	u := url.URL{Scheme: "ws", Host: peerAddr, Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Println("dial peer:", err)
		return err
	}
	que := make(ConnMsgQue, 1024)
	done := make(chan struct{})
	go d.readPeer(c, que, done)
	go d.writeMessage(c, que, done)
	return nil
}

func (d *DRPC) readPeer(c *websocket.Conn, que ConnMsgQue, done chan struct{}) {
	d.registerNotify(que)
	for {
		ty, err := d._readMessage(c, que)
		if err != nil {
			close(done)
			break
		}
		if ty == TypeSub {
			defer func() {
				log.Println("regist sub clear func")
				d.mtxt.Lock()
				for k, subs := range d.TopicSub {
					if _, ok := subs[que]; ok {
						delete(d.TopicSub[k], que)
						log.Println("delete topic a sub: ", k)
					}
				}
				d.mtxt.Unlock()
			}()
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
func (d *DRPC) registerNotify(que ConnMsgQue) {
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

func (d *DRPC) updateClusterNetAddrs(netAddrInfo string) {
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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (d *DRPC) acceptConn(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	que := make(ConnMsgQue, 1024)
	done := make(chan struct{})
	go d.readMessage(c, que, done)
	go d.writeMessage(c, que, done)
	go d.timeoutDelete()
}

func (d *DRPC) drpcdClusterAddrs(w http.ResponseWriter, r *http.Request) {
	var netAddrs []string
	for addr := range d.ClusterAddr {
		netAddrs = append(netAddrs, addr)
	}
	if len(netAddrs) == 0 {
		netAddrs = append(netAddrs, d.addr)
	}
	info, _ := json.Marshal(netAddrs)
	w.Write(info)
}

func (d *DRPC) drpcdClusterTopics(w http.ResponseWriter, r *http.Request) {
	info := make(map[string][]string)
	for topic, clis := range d.TopicSub {
		for _, addr := range clis {
			info[topic] = append(info[topic], addr)
		}
	}
	msg, _ := json.MarshalIndent(info, " ", "	")
	w.Write(msg)
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
		log.Println("http call timeout: ", msg.Timeout)
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
	d.pub(msg, nil)

	w.Write(d.httpError(ErrCodeOK, ""))
}

func (d *DRPC) timeoutDelete() {

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

func (d *DRPC) writeMessage(c *websocket.Conn, que ConnMsgQue, done chan struct{}) {
	for {
		select {
		case msg := <-que:
			c.WriteJSON(msg)

		case <-done:
			return
		case <-d.stop:
			return
		}
	}
}

func (d *DRPC) readMessage(c *websocket.Conn, que ConnMsgQue, done chan struct{}) {
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
		if ty == TypeSub {
			defer func() {
				log.Println("regist sub clear func")
				d.mtxt.Lock()
				for k, subs := range d.TopicSub {
					if _, ok := subs[que]; ok {
						delete(d.TopicSub[k], que)
						log.Println("delete topic a sub: ", k)
					}
				}
				d.mtxt.Unlock()
			}()
		}
		select {
		case <-done:
			return
		case <-d.stop:
			return
		default:
		}
	}
}

func (d *DRPC) _readMessage(c *websocket.Conn, que ConnMsgQue) (int, error) {
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
	case TypeSub:
		log.Println("recv TypeSub")
		d.sub(c.RemoteAddr().String(), msg, que)
	case TypePub:
		d.pub(msg, que)
	}
	return msg.Type, nil
}

func (d *DRPC) regNotify(msg DRpcMsg, que ConnMsgQue) {
	d.NotifyMember = append(d.NotifyMember, que)
	// 收到有集群成员注册通知， 一开始就把所有的注册函数给它
	nameList := d.MethodOwner.Methods()
	for _, name := range nameList {
		m := DRpcMsg{
			Type:     TypeReg,
			FuncName: name,
		}
		que <- m
	}
	topicList := d.getAllSubTopic()
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

func (d *DRPC) getAllSubTopic() []string {
	d.mtxt.Lock()
	defer d.mtxt.Unlock()
	var topicList []string
	for topic := range d.TopicSub {
		topicList = append(topicList, topic)
	}
	return topicList
}

func (d *DRPC) notify(msg DRpcMsg, curQue ConnMsgQue) {
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

func (d *DRPC) unReg(msg DRpcMsg) {

	d.MethodOwner.Del(msg.FuncName)
}

func (d *DRPC) update(msg DRpcMsg) {
	var netAddrs []string
	json.Unmarshal([]byte(msg.Body), &netAddrs)

	for _, addr := range netAddrs {
		d.ClusterAddr[addr] = struct{}{}
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
	d.mtxt.Lock()
	defer d.mtxt.Unlock()

	if _, ok := d.TopicSub[msg.FuncName]; !ok {
		d.TopicSub[msg.FuncName] = make(map[ConnMsgQue]string)
	}
	d.notify(msg, que)
	d.TopicSub[msg.FuncName][que] = subAddr
}

func (d *DRPC) pub(msg DRpcMsg, que ConnMsgQue) {
	d.mtxt.Lock()
	defer d.mtxt.Unlock()

	subs, ok := d.TopicSub[msg.FuncName]
	if !ok {
		return
	}
	for sub := range subs {
		if que == sub {
			continue
		}
		select {
		case sub <- msg:
		case <-time.After(time.Millisecond * time.Duration(msg.Timeout)):
			log.Println("publish timeout: ", msg.Timeout)
		}
	}
}

func (d *DRPC) writeError(msg DRpcMsg, que ConnMsgQue) {
	msg.Type = TypeResp
	que <- msg
}
