package drpc

import (
	"encoding/json"
	"sync"
)

// Provider imp
type Provider struct {
	// 方法名与对应注册者提供的消息队列
	MethodOwner map[string]ConnMsgQue
	MethodDoc   map[string]string
	mu          sync.RWMutex
}

// NewProvider create
func NewProvider() *Provider {
	p := &Provider{
		MethodOwner: make(map[string]ConnMsgQue),
		MethodDoc:   make(map[string]string),
	}
	return p
}

// Add method
func (p *Provider) Add(fnName, callDoc string, que ConnMsgQue) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.MethodOwner[fnName]; ok {
		return false
	}
	p.MethodOwner[fnName] = que
	p.MethodDoc[fnName] = callDoc
	return true
}

// Call method
func (p *Provider) Call(msg DRpcMsg) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	que, ok := p.MethodOwner[msg.FuncName]
	if !ok {
		return false
	}
	que <- msg
	return true
}

// Del method
func (p *Provider) Del(fnName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.MethodOwner[fnName]; !ok {
		return
	}
	delete(p.MethodOwner, fnName)
	delete(p.MethodDoc, fnName)
}

// DelByQue method
func (p *Provider) DelByQue(que ConnMsgQue) []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var fnNames []string
	for fnName, t := range p.MethodOwner {
		if que != t {
			continue
		}
		delete(p.MethodOwner, fnName)
		delete(p.MethodDoc, fnName)
		fnNames = append(fnNames, fnName)
	}
	return fnNames
}

// Methods method
func (p *Provider) Methods() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	ms := make([]string, 0, len(p.MethodOwner))
	for fnName := range p.MethodOwner {
		ms = append(ms, fnName)
	}
	return ms
}

// Docs method
func (p *Provider) Docs() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()

	docs, _ := json.MarshalIndent(p.MethodDoc, " ", "	")
	return docs
}
