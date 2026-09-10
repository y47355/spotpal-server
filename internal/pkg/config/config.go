// Package config 加载与解析启动配置（环境变量 + 命令行参数）。
package config

import (
	"flag"
	"os"
	"strconv"
)

// Config 服务端启动配置。
type Config struct {
	HTTPAddr  string // HTTP 监听地址（含 WS 升级）
	DataDir   string // SQLite 数据目录
	TokenKey  string // JWT 签名密钥
	LLM       string // llm 实现选择：mock|hunyuan|gpt
	RateLimit int    // 每 IP+UID 令牌桶速率（req/s）
	Seed      bool   // 启动后执行 seed（演示模式）
}

// Load 从命令行与环境变量装配配置。
func Load() *Config {
	c := &Config{}
	flag.StringVar(&c.HTTPAddr, "http", env("SPOTPAL_HTTP", ":8080"), "HTTP/WS 监听地址")
	flag.StringVar(&c.DataDir, "data", env("SPOTPAL_DATA", "./data"), "数据目录（spotpal.db 所处）")
	flag.StringVar(&c.TokenKey, "token-key", env("SPOTPAL_TOKEN_KEY", "dev-secret-change-me"), "JWT 签名密钥")
	flag.StringVar(&c.LLM, "llm", env("SPOTPAL_LLM", "mock"), "LLM 实现：mock | hunyuan | gpt")
	flag.BoolVar(&c.Seed, "seed", false, "启动后写入演示数据")
	rl, _ := strconv.Atoi(env("SPOTPAL_RATE_LIMIT", "100"))
	flag.IntVar(&c.RateLimit, "rate-limit", rl, "限流速率 req/s")
	flag.Parse()
	return c
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
