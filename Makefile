# 搭趣 SpotPal · 服务端 Makefile（零容器）
GO ?= go
BIN := spotpal-server
PORT ?= 8080

.PHONY: dev build seed test clean

## 本机直接跑（默认 :8080，数据在 ./data/spotpal.db）
dev:
	$(GO) run ./cmd/server -http :$(PORT)

## 带 seed 演示数据跑（3 用户 / 1 搭局 / 2 轮协商）
demo:
	$(GO) run ./cmd/server -http :$(PORT) -seed

## 构建单二进制（纯 Go SQLite，无 CGO，可交叉编译）
build:
	CGO_ENABLED=0 $(GO) build -o $(BIN) ./cmd/server

## 全量测试（SQLite 临时文件毫秒级集成）
test:
	$(GO) test ./...

clean:
	rm -f $(BIN) && rm -rf data
