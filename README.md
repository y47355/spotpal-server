# 搭趣 SpotPal · 服务端

> 基于兴趣 + 位置的搭子匹配，**AI 代协商**为核心差异化的轻社交产品。
> 单二进制 + 单数据文件，零基础设施，一条命令全链路可跑。

## 快速开始

```bash
make dev          # 本机跑（默认 :8080）
make demo         # 带 seed 演示数据跑（3 用户 / 1 搭局 / 2 轮协商）
make build        # 构建单二进制（CGO_ENABLED=0，可交叉编译）
make test         # 全量测试（SQLite 临时文件毫秒级）
```

唯一环境要求：**Go 1.22，没了**。没有容器、没有数据库服务、没有 CGO。

```bash
./spotpal-server -http :8080 -seed    # 构建产物即部署
cp data/spotpal.db backup.db          # 备份 = 停服后复制（或 VACUUM INTO 在线热备）
```

## 架构

```
Android / iOS / 小程序 ──HTTP :8080 / WS──▶ spotpal-server（Go 单进程）
                                                 │
                                                 ▼
                                            spotpal.db（SQLite 单文件）
```

- **存储**：SQLite（`modernc.org/sqlite` 纯 Go 驱动）· WAL 模式 · 单写多读
  （`SetMaxOpenConns(1)` 写串行 + 只读并行）
- **模式**：模块化单体（user / match / squad / negotiate / im / pay / credit / risk）
  · 模块间只依赖 interface + 事件契约，禁止 import 对方内部包
- **事件**：进程内总线 + outbox 表（事件先落库再投递，崩溃不丢；
  失败×3 → 死信）
- **超时**：timeouts 表 + 30s 轮询认领（单写者模型下天然无竞争）
- **支付**：初版**纯记账**（`PayLaterLedger`）——结算单只是账面记录，
  费用线下自理，零资金合规面；接微信分账时换 `pay.Ledger` 实现类
- **LLM**：`llm.TimeWindowResolver` 接口，mock 实现（时间窗交集 +
  费用模板，模拟 1~2s 延迟）；联调切真实 API 换实现类

## API（11 端点）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/auth/login` | 手机号 + 验证码登录（演示模式任意 6 位） |
| POST | `/v1/pool/push` | 上推入池（幂等） |
| GET | `/v1/pool` | 候选池列表 |
| DELETE | `/v1/pool/{candidateId}` | 移除候选 |
| POST | `/v1/negotiation/release` | 释放委托，AI 开始协商 |
| GET | `/v1/negotiation/{id}` | 协商详情（会话+轮次） |
| POST | `/v1/negotiation/{id}/feedback` | 方案反馈（ok=定稿） |
| POST | `/v1/squad/{id}/confirm` | 确认成局（乐观锁） |
| POST | `/v1/squad/{id}/checkin` | 打卡结算（纯记账） |
| GET | `/v1/feed/recommend` | 推荐流（tag/vec/geo 三路召回） |
| GET | `/v1/ws/messages?after_seq=` | WS 断线补拉 |

响应信封 `{code, msg, data, req_id}`；错误码段 1xxx 通用 / 2xxx 用户 /
3xxx 搭局 / 4xxx 协商 / 5xxx 结算。WS 信封 `{type, seq, payload, ts}`，
断线按 `last_seq` 补拉。

## 目录

```
cmd/server/          入口
internal/api/        HTTP 路由 + 中间件（鉴权/限流/trace）
internal/ws/         WebSocket 网关（连接管理/seq/补拉）
internal/module/     8 业务模块
internal/platform/   llm / sms（均可 mock）
internal/bus/        事件总线 + outbox 投递器
internal/timeout/    超时轮询认领
internal/db/         SQLite 打开/迁移
migrations/          SQL（SQLite 方言）
```

## 演示数据

`make demo` 或 `-seed` 写入：阿叶（羽毛球/艺术展）、阿哲（羽毛球/电影）、
小鹿（艺术展）；羽毛球搭局（NEGOTIATING）+ 2 轮协商记录。

```bash
# 冒烟
curl http://localhost:8080/healthz
curl "http://localhost:8080/v1/feed/recommend?uid=u_demo_ye"
curl "http://localhost:8080/v1/pool?uid=u_demo_ye"
curl "http://localhost:8080/v1/negotiation/ngt_demo_1?uid=u_demo_ye"
curl -X POST "http://localhost:8080/v1/squad/sq_demo_1/confirm?uid=u_demo_ye"
```

## 演进路径

| 级 | 触发器 | 动作 |
|---|---|---|
| 1 | 写 QPS >500 / 多副本 / DAU >2w | SQLite → PG（pgloader + 方言适配层） |
| 2 | 召回延迟超标 / 缓存命中率降 | 上 ES/Milvus/Redis（查询接口不变） |
| 3 | DAU >5w / 多城 | 模块化单体 → 微服务（模块边界已按拆分预设） |

## 相关仓库

| 仓库 | 说明 |
|---|---|
| [spotpal-android](https://github.com/y47355/spotpal-android) | Android 客户端（Kotlin + Compose + MVI，上推托付手势） |
| [spotpal-docs](https://github.com/y47355/spotpal-docs) | 技术架构 + 客户端/服务端详细设计 + 高保真原型 |

## License

MIT

## License

MIT
