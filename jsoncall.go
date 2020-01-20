package drpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// JsonCall json方式的注册与调用封装
type JsonCall struct {
	funcMap sync.Map
}

type funcType struct {
	// 函数对象
	fn interface{}

	// 参数列表结构
	pst interface{}

	// 返回值列表结构
	rst interface{}
}

// Reg: 函数名 功能函数 参数列表（指针） 返回值列表（指针）
func (jc *JsonCall) Reg(fnName string, fn, pst, rst interface{}) {
	if _, ok := jc.funcMap.Load(fnName); ok {
		panic("方法名已经被注册: " + fnName)
	}
	jc.funcMap.Store(fnName, funcType{fn, pst, rst })
}

func (jc *JsonCall) Call(fnName string, arg []byte) ([]byte, error){
	v, ok := jc.funcMap.Load(fnName)
	if !ok {
		panic("调用的方法名不存在: " + fnName)
	}
	fnInfo := v.(funcType)

	pst := reflect.New(reflect.TypeOf(fnInfo.pst).Elem()).Interface()
	rst := reflect.New(reflect.TypeOf(fnInfo.rst).Elem()).Interface()

	err := json.Unmarshal(arg, pst)
	if err != nil {
		return nil, err
	}

	err = jc.call(fnInfo.fn, pst, rst)

	d, _ := json.Marshal(rst)
	return d, err
}

// 注意： pst rst 传进来的是指针类型
func (jc *JsonCall) call(fn interface{}, pst, rst interface{}) error{
	// 准备参数
	dType := reflect.TypeOf(pst).Elem()
	dValue := reflect.ValueOf(pst).Elem()

	num := dType.NumField()
	if reflect.TypeOf(fn).NumIn() != num {
		return errors.New("参数个数不匹配")
	}
	params := make([]reflect.Value, num)
	for i := 0; i < num; i++ {
		params[i] = dValue.FieldByName(dType.Field(i).Name)
	}

	// 调用功能函数
	rs := reflect.ValueOf(fn).Call(params)

	// 填充结果
	return fillResult(fn, rs, rst)
}

// 结果数组  结果列表
func fillResult(fn interface{}, vRet []reflect.Value, rst interface{})  error{
	dType := reflect.TypeOf(rst).Elem()
	dValue := reflect.ValueOf(rst).Elem()

	if dType.NumField() < len(vRet) {
		return errors.New("结果与返回值列表个数不匹配")
	}
	for i, r := range vRet {
		fieldValue := dValue.FieldByName(dType.Field(i).Name)
		if !fieldValue.CanSet(){
			panic(fmt.Sprintf("返回值结构字段 %d 不能被赋值", i))
		}
		if reflect.TypeOf(fn).Out(i).String() != "error"  {
			fieldValue.Set(r)
		}else {
			var code bool
			var errMsg string
			if r.IsNil() {
				code = true
			}else{
				errMsg = r.Interface().(error).Error()
			}
			dValue.FieldByName(dType.Field(i).Name).Set(reflect.ValueOf(code))
			dValue.FieldByName(dType.Field(i+1).Name).Set(reflect.ValueOf(errMsg))
		}
	}
	return nil
}
