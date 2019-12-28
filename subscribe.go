package drpc

import (
	"encoding/json"
	"sync"
	"log"
	"time"
)

// Subscribe imp
type Subscribe struct {
	TopicSub map[string]map[string]ConnMsgQue
	mu     sync.RWMutex
}

// NewSubscribe create
func NewSubscribe() *Subscribe {
	su := &Subscribe{
		TopicSub:  make(map[string]map[string]ConnMsgQue),
	}
	return su
}

// Sub method
func (su *Subscribe) Sub(topic, addr string, que ConnMsgQue) {
	su.mu.Lock()
	if _, ok := su.TopicSub[topic]; !ok {
		su.TopicSub[topic] = make(map[string]ConnMsgQue)
	}
	su.TopicSub[topic][addr] = que
	su.mu.Unlock()
}

// Pub method
func (su *Subscribe) Pub(msg DRpcMsg, addr string) {
	su.mu.Lock()
	defer su.mu.Unlock()

	subs, ok := su.TopicSub[msg.FuncName]
	if !ok {
		return
	}
	for t, sub := range subs {
		if t == addr {
			continue
		}
		select {
		case sub <- msg:
		case <-time.After(time.Millisecond * time.Duration(msg.Timeout)):
			log.Println("publish timeout: ", msg.Timeout)
		}
	}
}

// Del method
func (su *Subscribe) Del(spTopic, addr string) {
	su.mu.Lock()
	defer su.mu.Unlock()

	// 删除指定topic此客户端的订阅
	if spTopic != "" {
		_, ok := su.TopicSub[spTopic]
		if ok {
			log.Println("delete specify topic sub: ", spTopic, addr)
			delete(su.TopicSub[spTopic], addr)
		}
		return
	}
	// 删除所有topic此客户端的订阅
	for topic, subs := range su.TopicSub {
		if _, ok := subs[addr]; ok {
			log.Println("delete all topic sub: ", topic, addr)
			delete(su.TopicSub[topic], addr)
			break
		}
	}
}

// Info method
func (su *Subscribe) Info() []byte {
	su.mu.Lock()
	defer su.mu.Unlock()

	vTmp := make(map[string][]string)
	for topic, subs := range su.TopicSub {
		vTmp[topic] = make([]string, 0)
		for addr := range subs {
			vTmp[topic] = append(vTmp[topic], addr)
		}
	}
	info, _ := json.MarshalIndent(vTmp, " ", "	")
	return info
}

// Topics method
func (su *Subscribe) Topics() []string {
	su.mu.Lock()
	defer su.mu.Unlock()

	vTmp := make([]string, 0)
	for topic := range su.TopicSub {
		vTmp = append(vTmp, topic)
	}
	return vTmp
}



