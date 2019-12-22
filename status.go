package drpc

const (
	// ErrCodeOK 成功
	ErrCodeOK = iota + 1

	// ErrCodeParamFormatError 参数格式错误
	ErrCodeParamFormatError

	// ErrCodeRepTimeout 请求功能提供者超时
	ErrCodeRepTimeout

	// ErrCodeFunctionNotExist 请求功能不存在
	ErrCodeFunctionNotExist

	// ErrCodeProviderDisconnect 功能提供者掉线
	ErrCodeProviderDisconnect

	// ErrCodeFunctionBeRegistered 功能已经被注册
	ErrCodeFunctionBeRegistered
)

const (
	// TypeUnknow 未知类型
	TypeUnknow = iota + 1

	// TypeReg 请求注册成为提供者
	TypeReg

	// TypeUnReg 请求解注册
	TypeUnReg

	// TypeRegNotify 请求注册通知
	TypeRegNotify

	// TypeCall 请求调用
	TypeCall

	// TypeResp 应答请求
	TypeResp

	// TypeUpdateNetAddr 更新网络地址
	TypeUpdateNetAddr
)

// 注册功能者与调用功能者需要初始化字段如下：
// 注册者： Type, FuncName
// 调用者： Type, UniqueID, FuncName, Timeout, Body
// 应答者： Type, UniqueID, ErrCode, Body

// DRpcMsg 客户端与服务端通信类型
type DRpcMsg struct {
	// Type 可以是注册功能、请求调用、请求应答
	Type int

	// UniqueID 唯一值，标识此次对话
	UniqueID int64

	// ErrCode 错误代码，一般是应答方赋值
	ErrCode int

	// FuncName 请求调用或者注册的函数名
	FuncName string

	// Timeout 请求调用时超时时间(毫秒)
	Timeout int64

	// 请求调用时是参数，应答时是返回值，一般是json格式
	Body string

	// 注册的情况下功能调用与返回值描述
	Doc string
}

// DRpcMsgHTTP 客户端是http
type DRpcMsgHTTP struct {

	// ErrCode 错误代码，一般是应答方赋值
	ErrCode int

	// 请求调用时是参数，应答时是返回值，一般是json格式
	Body string
}
