package drpc

import (
	"log"
)

// Logger 日志模块
type Logger interface {
	Info(v ...interface{})
	Warn(v ...interface{})
	Error(v ...interface{})
	Fatal(v ...interface{})
}

// DefLogger 默认日志
type DefLogger struct {}

// Info 默认使用标准库的log
func (lg *DefLogger) Info(v ...interface{}) {
	log.Println(v...)
}

// Warn 默认使用标准库的log
func (lg *DefLogger) Warn(v ...interface{}) {
	log.Println(v...)
}

// Error 默认使用标准库的log
func (lg *DefLogger) Error(v ...interface{}) {
	log.Println(v...)
}

// Fatal 默认使用标准库的log
func (lg *DefLogger) Fatal(v ...interface{}) {
	log.Fatal(v...)
}


