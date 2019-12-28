package drpc

import (
	"log"
	"sync"
	"time"
)

// WaitGroup imp
type WaitGroup struct {
	waitGrp  []*WaitImp
	waitCNum int
}

// NewWaitGroup create
func NewWaitGroup(waitCNum int) *WaitGroup {
	wg := &WaitGroup{
		waitCNum: waitCNum,
		waitGrp:  make([]*WaitImp, waitCNum),
	}
	log.Println("wait group member:", waitCNum)
	for i := 0; i < waitCNum; i++ {
		wg.waitGrp[i] = NewWaitImp()
	}
	return wg
}

// Get 获得id对应的等待容器
func (wg *WaitGroup) Get(id int64) *WaitImp {
	return wg.waitGrp[int(id%int64(wg.waitCNum))]
}

// ClearExpired 清理超时
func (wg *WaitGroup) ClearExpired() {
	for _, wait := range wg.waitGrp {
		wait.Expired()
	}
}

// WaitImp IMP
type WaitImp struct {
	sync.Mutex
	waitC map[int64]WaitVal
}

// NewWaitImp create
func NewWaitImp() *WaitImp {
	w := WaitImp{
		waitC: make(map[int64]WaitVal),
	}
	return &w
}

// Add method
func (wr *WaitImp) Add(id int64, val WaitVal) {
	wr.Lock()
	wr.waitC[id] = val
	wr.Unlock()
}

// Del method
func (wr *WaitImp) Del(id int64) {
	wr.Lock()
	delete(wr.waitC, id)
	wr.Unlock()
}

// GetAndDel method
func (wr *WaitImp) GetAndDel(id int64) (WaitVal, bool) {
	wr.Lock()
	defer wr.Unlock()
	if val, ok := wr.waitC[id]; ok {
		delete(wr.waitC, id)
		return val, ok
	}
	return WaitVal{}, false
}

// Expired method
func (wr *WaitImp) Expired() {
	wr.Lock()
	curTm := time.Now()
	for id, wait := range wr.waitC {
		if curTm.Sub(wait.createTm).Milliseconds() < wait.msg.Timeout {
			continue
		}
		log.Println("delete timeout id: ", id)
		delete(wr.waitC, id)
		wait.msg.ErrCode = ErrCodeRepTimeout
		wait.msg.Type = TypeResp
		wait.conn <- wait.msg
	}
	wr.Unlock()
}

// WaitVal STRUCT
type WaitVal struct {
	// 创建时间
	createTm time.Time
	// 原始消息
	msg DRpcMsg
	// 等待应答的chan
	conn chan DRpcMsg
}
