package drpc

import "net/http"

type HandlerFunc  func(w http.ResponseWriter, r *http.Request, fnCall func(msg DRpcMsg) []byte)

type Middleware interface {
	GetMapHandler() map[string]HandlerFunc
}


type MonitorFunc func(msg DRpcMsg)
type MonitorFuncChain []MonitorFunc
