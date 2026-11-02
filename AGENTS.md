# AGENTS.md

## 项目

观澜（Telegram Bot `@GuanLanAI_bot`）：Go 编写的 AI 辅助交易机器人。Phase 0 骨架已完成：Go module、配置、SQLite 迁移、健康检查、Dockerfile、Compose 和 VPS 部署目录；Telegram、行情、LLM 尚未接入。详细规划、阶段拆分和验收标准见 `docs/PLAN.md`，实现时按该文档推进，重大偏离先更新它。

常用命令：

```sh
cp .env.example .env   # 首次
make fmt-check vet test
make build
make run               # 自动加载 .env
make docker            # 本机架构镜像
guanlanai backup       # VACUUM INTO 备份，容器内用 docker compose exec
```

## 文档语言

- 所有文档（README、设计说明、本文件的后续更新）一律使用中文。
- 代码标识符、环境变量名、外部 API 字段和库名保持原文，不要翻译。

## 产品边界（早期版本必须遵守）

- 只做 Telegram 私聊，不提前做 Web/App，也不做群组。
- 用户自带 API Key，初期走 OpenAI Chat Completions 兼容协议；支持每用户自定义兼容 `base_url`，其他厂商通过 provider 接口后加。
- 当前只做加密货币，输入单个币种代码（如 `BTC`、`ETH`）输出交易观点；暂不做股票（美股/A 股/港股等），也**不下单**。
- USDC 充值/高级功能只预留接口，不实现；币安、Gate 等交易所执行同理，后续再接。
- 会话管理尽量做；成本过高时，至少按 chat 保存最近 N 轮，超限时提示或压缩，不能无限拼接历史。
- 用户消息默认保留 30 天；`/reset` 立即删除当前会话消息和摘要。

## 已定技术选型

- 单一 Go module：`github.com/guanlanai/guanlanai`，Go 1.26+（`modernc.org/sqlite` 当前版本要求）；不要建 `go.work`（已被 gitignore）。
- Telegram：`github.com/go-telegram/bot`。
- 行情：Binance USDT 现货为主，默认 `BINANCE_BASE_URL=https://data-api.binance.vision`；`api.binance.com` 在当前 VPS 返回 451。Phase 3 先只实现 Binance，CoinGecko 兜底待补；MVP 不接股票行情源。
- LLM：官方 `github.com/openai/openai-go` + 自写薄 Agent 循环；走 Chat Completions 兼容协议，不要依赖 Responses API。不要引入 langchaingo、eino 等第二套 Agent 框架。
- 模型列表来自用户 endpoint 的 `GET /models`，不是写死的；`STRICT_MODEL_LIST=true`（默认）要求模型必须出现在该列表，代理不支持 `/models` 时才设为 false。
- `gpt-5` 等推理模型的 `completion_tokens` 包含 reasoning tokens；`MAX_OUTPUT_TOKENS` 生产环境用 4000，避免 JSON 被截断。
- 配置走环境变量，本地用 `.env`（已 gitignore）。约定名称：`TELEGRAM_BOT_TOKEN`、`APP_MASTER_KEY`、`OPENAI_API_KEY`（仅本地开发回退；正常流程使用用户自己的 Key）。

## 部署约定

- 生产目标：单台甲骨文免费 VPS + Docker Compose + SQLite volume；long polling，不需要入站 HTTP。
- 本机可用 `ssh o1` 登录 VPS；实测 Ubuntu 26.04 `aarch64`、A1.Flex 1 OCPU/5.8GB、region `iad`。Docker 29.1.3 / Compose 2.40.3 已安装。
- Phase 0 已部署：`guanlanai-prod-bot-1`、`guanlanai-staging-bot-1` 均 healthy，容器用户 `PUID/PGID=1001`，无宿主端口发布。
- Phase 1 已部署到 prod/staging：镜像 `guanlanai:phase1`，`@GuanLanAI_bot` 和 `@ax_ha_1_bot` 均已连接。
- Phase 2 已部署到 prod/staging：镜像 `guanlanai:phase2`，包含 Key 加密、`/key`、`/baseurl`、`/model`、`/mode`、SSRF 防护；两套环境均已用真实 Key 验证。
- Phase 3 已部署到 prod/staging：镜像 `guanlanai:phase3`，Binance 行情 + 技术指标 + `gpt-5-mini` 结构化观点；staging 已验证 BTC/SOL/BNB，prod 已切换。
- Phase 4 已部署到 prod/staging：镜像 `guanlanai:phase4`，会话上下文、追问、摘要裁剪、限流、usage 记录；staging 已验证，prod 已切换。
- Phase 5 已部署到 prod/staging：镜像 `guanlanai:phase5`，新增 `guanlanai backup`（VACUUM INTO）、观点长度上限和测试补全；备份已在 staging 验证。
- Oracle Security List/NSG 只开 SSH；不要暴露 8080，健康检查在容器内或用 `127.0.0.1`。
- 测试 bot 可同机：独立 `.env`、Bot Token、`APP_MASTER_KEY`、SQLite、volume、compose project；不共享端口和密钥。
- 镜像必须匹配 VPS 架构（Oracle Ampere 是 arm64）；优先构建 amd64/arm64 多架构镜像，或按 `uname -m` 在 VPS 构建。
- 当前 VPS 上 `api.binance.com` 返回 451，`data-api.binance.vision` 返回 200；默认走 vision，CoinGecko 兜底。
- 111（rpcbind）在监听；无 NFS 需求时禁用，并确认 Security List 未放行。

## 目录约定

已存在：

- `cmd/bot/` — 进程入口、信号处理
- `internal/app/` — 生命周期、依赖装配
- `internal/config/` — 环境变量解析与校验
- `internal/health/` — `/healthz`、`/readyz`
- `internal/storage/sqlite/` — SQLite 打开、迁移、用户配置读写
- `internal/telegram/` — long polling、命令路由、per-chat 串行队列
- `internal/render/` — HTML 转义、消息拆分、发送
- `internal/text/` — 中文文案
- `internal/secrets/` — AES-256-GCM 用户 Key 加密
- `internal/netguard/` — 自定义 base_url 的 SSRF 防护
- `internal/llm/` — Validator 接口和错误类型
- `internal/llm/openai/` — OpenAI 兼容 `/models` 校验与模型列表
- `internal/user/` — 用户模型与 Store 接口
- `migrations/` — 嵌入二进制的 SQL
- `deploy/{prod,staging}/` — VPS Compose 模板

待创建（按此边界放置，不要提前建空包）：

- `internal/agent/` — 会话循环、工具调用、上下文压缩
- `internal/market/` — 代码解析、行情/数据源
- `internal/session/` — 按 chat/user 的会话与历史
- `internal/billing/` — 付费/配额接口（占位）
- `internal/trade/` — 交易所执行接口（占位，默认关闭）

## 扩展点

- 新增模型厂商：实现 `internal/llm.Provider`，不要改 agent 核心。
- 新增付费：实现 `internal/billing` 接口；未配置时必须回退到免费额度，不能让进程启动失败。
- 新增下单：实现 `internal/trade.Executor`，默认 nil/disabled；真实下单必须显式开关 + 用户确认。
- 行情数据源同理放在 `internal/market` 的接口后，不要把交易所 SDK 直接塞进 Telegram handler。

## 安全

- 永不记录或回显用户 API Key、Bot Token；日志中打码。
- 用户 Key 必须 AES-256-GCM 加密落库，主密钥来自 `APP_MASTER_KEY`；禁止明文存储。
- 用户自定义 `base_url` 必须做 SSRF 防护：仅 HTTPS、拒绝私网/回环/链路本地地址、禁止重定向、限制响应大小和超时。
- `.env` 和任何密钥都不入库。
- 交易观点必须附风险提示，不能表述为投资建议；暂不专门做合规审查，但对外文案保留免责声明。

## 验证

```sh
make fmt-check                          # 必须无输出
make vet
make test
make test-race
make build
go test ./internal/config -run TestName # 单个测试
```
