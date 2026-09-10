// Package log 结构化启动日志（stdlib log 精简封装，零依赖）。
package log

import (
	"log/slog"
	"os"
)

// L 全局 logger；main 中 Init 后方可使用。
var L = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

// Init 按需调整级别（debug 模式）。
func Init(debug bool) {
	lvl := slog.LevelInfo
	if debug {
		lvl = slog.LevelDebug
	}
	L = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
