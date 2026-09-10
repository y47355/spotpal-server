// Package sms 短信平台接口与日志实现（详细设计 §2.4：验证码打印到 server 日志）。
package sms

import (
	"github.com/spotpal/spotpal-server/internal/pkg/log"
)

// Sender 短信发送接口。
type Sender interface {
	Send(phone, code string) error
}

// LogSender 初版实现：验证码只写日志（开发/演示用）。
type LogSender struct{}

// NewLogSender 构造。
func NewLogSender() *LogSender { return &LogSender{} }

// Send 打印到 server 日志。
func (s *LogSender) Send(phone, code string) error {
	log.L.Info("sms code (dev mode)", "phone", phone, "code", code)
	return nil
}
