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
func (jc *JsonCall) call(fn interface{},  pst, rst interface{}) error{
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
	return fillResult(rs, rst)
}

// 结果数组  结果列表
func fillResult(vRet []reflect.Value, rst interface{})  error{
	dType := reflect.TypeOf(rst).Elem()
	dValue := reflect.ValueOf(rst).Elem()

	num := dType.NumField()
	if num != len(vRet) && num != len(vRet)+1 {
		return errors.New("结果与返回值列表个数不匹配")
	}
	for i, r := range vRet {
		fieldValue := dValue.FieldByName(dType.Field(i).Name)
		if !fieldValue.CanSet(){
			panic(fmt.Sprintf("返回值结构字段 %d 不能被赋值", i))
		}
		if v, ok := r.Interface().(error); !ok {
			fieldValue.Set(r)
		}else{
			// error的返回值转化为一个bool 与 一个string
			var b bool
			if v == nil {
				b = true
			}
			fieldValue.Set(reflect.ValueOf(b))
			dValue.FieldByName(dType.Field(i+1).Name).Set(reflect.ValueOf(v.Error()))
		}
	}
	return nil
}
