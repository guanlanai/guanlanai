# 观澜（GuanLanAI）项目规划

> 本文是实施蓝图，不是最终产品文档。已拍板的决策直接作为约束；标注“默认假设”的部分可以在开发前调整。

## 1. 目标与边界

### 1.1 一句话目标

在 Telegram 私聊中，用户配置自己的 OpenAI API Key 后，发送一个加密货币代码，机器人结合实时/近实时行情与确定性技术指标，输出结构化、可解释、带风险提示的交易观点。

### 1.2 MVP 必须做到

- Telegram 私聊可用，Bot 为 `@GuanLanAI_bot`。
- 用户自带 OpenAI Key，Key 加密存储，收到 Key 的消息立即删除。
- 支持每用户配置 OpenAI Chat Completions 兼容的 `base_url`（默认官方 endpoint），并对自定义地址做 SSRF 防护。
- 只支持加密货币，优先覆盖 Binance USDT 现货主流币种。
- 支持短线、波段两种分析模式，输出结构化观点。
- 支持追问，能记住当前标的和最近几轮对话。
- 有基础限流、超时、错误提示和成本上限。
- 甲骨文免费 VPS 单机 Docker 部署，SQLite 持久化，重启不丢用户配置和会话摘要。
- 全程不涉及真实下单。

### 1.3 MVP 明确不做

- 暂不做股票，包括美股、A 股、港股、日股等；先把加密货币跑通，后续再评估。
- 不做新闻、财报、社交情绪数据。
- 不做真实交易、不接交易所私有 API、不托管用户资金。
- 不做 USDC 充值/扣费，只保留接口和用量记录。
- 不做 Web/App、不做多实例、不做复杂管理后台。
- 不做群组共享会话；群组支持按成员隔离，每个成员在同一群固定一条会话。
- 不承诺收益，不给出“稳赚”“必涨”类表述。

## 2. 已拍板的技术决策

| 项目 | 决策 |
| --- | --- |
| 语言/模块 | Go 1.26+（`modernc.org/sqlite` 当前版本要求），单 module `github.com/guanlanai/guanlanai` |
| Telegram | `github.com/go-telegram/bot`，MVP 用 long polling |
| LLM | 官方 `github.com/openai/openai-go` + 自写薄 Agent 循环；走 Chat Completions 兼容协议 |
| 兼容代理 | 每用户可配置 OpenAI 兼容 `base_url`；默认官方 endpoint |
| 市场 | 仅加密货币；股票后置 |
| 行情源 | Binance USDT 现货；默认 `data-api.binance.vision`（当前 VPS 上 `api.binance.com` 返回 451），CoinGecko 兜底为低优先级、暂不实现 |
| 存储 | 单机 SQLite，驱动用纯 Go 的 `modernc.org/sqlite`，避免 CGO |
| 用户 Key | AES-256-GCM 加密后存 SQLite；收到 Key 后删除 Telegram 消息 |
| 会话 | 私聊支持多会话；群组按成员隔离；消息默认保留 30 天；`/reset` 立即删除 |
| 部署 | 甲骨文免费 VPS 单机 + Docker Compose + volume；可同机再跑一套测试 bot |
| 镜像 | 支持 `linux/amd64` 与 `linux/arm64`（Oracle Ampere），或按 VPS 架构单独构建 |
| 环境隔离 | 生产/测试独立 `.env`、SQLite、主密钥、volume、compose project，不共享端口和密钥 |
| 观点格式 | 结构化、多周期；短线/波段可选 |
| 默认模型 | 便宜的小模型，模型列表可配置，用户可切换 |
| 访问方式 | 公开可用；靠“必须自带 Key + 限流 + 并发上限”控制滥用 |
| 合规 | 暂不专门做合规审查；保留免责声明与风险提示 |
| 新闻/情绪 | MVP 不做，后续再加 |

## 3. 用户流程

### 3.1 首次使用

1. 用户发送 `/start`。
2. Bot 发送欢迎语、能力边界、风险提示。
3. 若未配置 Key，Bot 引导 `/key`；需要第三方代理时先用 `/baseurl` 配置兼容地址。
4. 用户发送 `/key`，Bot 提示“下一条消息发送 Key，收到后立即删除”。
5. 用户发送 Key：
   - Bot 立即删除该消息（失败则提示用户自行撤回）。
   - 按用户当前 `base_url` 校验 Key（优先 `GET {base_url}/models`，失败再用最小 Chat Completions 请求校验）。
   - 校验通过后加密入库，回复掩码后的 Key（如 `sk-...a1b2`）和当前 endpoint 域名。
   - 校验失败不落库，返回明确原因。
6. 用户发送 `BTC` 或 `ETH`，进入分析。

### 3.2 日常分析

1. 用户发送代码，或 `/analyze BTC`。
2. Bot 先回一条“正在分析 BTC（加密 · 波段）…”。
3. 并行拉取多周期行情，计算指标，调用 LLM。
4. 校验结构化输出，渲染成观点卡片。
5. 若数据不足、模型输出异常或行情源失败，明确说明，不编造价格。

### 3.3 追问

- 用户：“那止损放哪？”、“如果跌破支撑呢？”。
- 沿用当前会话的标的、模式、最近一次分析结果。
- 追问走有界 Agent 工具循环，可重新拉行情或补算周期，最多 3 轮工具调用。
- 用户切换标的时更新会话上下文。

### 3.4 命令

| 命令 | 作用 |
| --- | --- |
| `/start` | 欢迎、风险提示、使用引导 |
| `/help` | 命令与示例 |
| `/key` | 配置/更新 OpenAI Key |
| `/key delete` | 删除已存 Key |
| `/baseurl` | 设置 OpenAI 兼容代理地址；`/baseurl reset` 恢复官方 endpoint |
| `/model` | 查看并切换允许的模型；代理场景可拉取 `/models` 或手动输入模型名 |
| `/mode` | 切换短线/波段 |
| `/status` | Key 状态（掩码）、endpoint 域名、当前模型/模式、今日用量 |
| `/reset` | 清空当前会话上下文 |
| `/cancel` | 取消等待输入 Key 等临时状态 |
| `/analyze <代码>` | 显式分析（也支持直接发代码） |

### 3.5 输出卡片（示例）

```text
BTC/USDT · 加密 · 波段
🟢 偏多（中等信心）
现价：68,432.10 USDT（UTC 2026-11-02 15:30）
关键位：支撑 66,800 / 65,200；阻力 70,500 / 72,000
计划：入场 67,800–68,600；止损 65,200；目标 70,500 / 72,000；盈亏比 2.1
理由：
- 日线站上 EMA20/50，趋势向上
- 1h RSI 回落后重新走强，MACD 柱状图转正
- 放量突破近 20 根 K 线震荡区间上沿
风险：
- 加密市场 24/7 交易，突发波动可能导致滑点
- 若跌破 65,200 则观点失效
数据：Binance；行情可能延迟
仅供参考，不构成投资建议。
```

中性观点不强行给交易计划，显示“观望，等待突破/回踩确认”。

## 4. 系统架构

> 运行时架构、首次分析流程、追问工具循环和部署拓扑见 [ARCHITECTURE.md](ARCHITECTURE.md)。

```text
Telegram long polling
        │
        ▼
Update worker pool（按 chat 串行）
        │
        ├── 命令路由（/start /key /baseurl /model /mode /status /reset）
        │
        ├── 代码解析 / 意图识别
        │
        ├── 分析管道（确定性）
        │     ├── market.Provider（Binance；CoinGecko 低优先级）
        │     ├── analysis（EMA/RSI/MACD/ATR/支撑阻力）
        │     └── 多周期指标摘要 JSON
        │
        ├── Agent（LLM）
        │     ├── llm.Provider（OpenAI 实现）
        │     ├── 结构化输出校验 / 一次修复
        │     └── 追问工具循环（最多 3 轮）
        │
        ├── session（SQLite：用户、会话、消息、摘要）
        ├── billing.Service（Noop，记录用量）
        └── trade.Executor（Disabled，不注册任何交易工具）
```

### 4.1 包依赖规则

- `cmd/bot`：只做 flag、配置加载、调用 `internal/app`。
- `internal/app`：装配依赖、启动 Telegram/HTTP、处理优雅退出。
- `internal/telegram`：更新处理、命令、回调、消息发送；不直接依赖具体行情/LLM 实现。
- `internal/agent`：编排分析/追问；依赖 `llm`、`market`、`analysis`、`session`、`billing`、`trade` 的接口。
- `internal/llm/openai`：只依赖 OpenAI SDK。
- `internal/market/{yahoo,binance,coingecko}`：HTTP 适配器，统一输出标准 K 线。
- `internal/analysis`：纯函数，无 I/O，方便测试。
- `internal/metrics`：Prometheus registry 和业务指标；不依赖具体行情/LLM 实现。
- `internal/storage/sqlite`：实现 repository 接口。
- 接口定义在消费方（Go 惯例），避免循环依赖。

### 4.2 并发模型

- Telegram 更新进入固定大小 worker pool（如 32）。
- 同一个 chat 串行处理，避免上下文和消息顺序错乱；可用 per-chat 队列，队列满时回复“上一条还在处理”。
- 全局 LLM 并发上限（如 16），行情并发上限（如 8），用带超时的信号量。
- 单次分析总超时 75s：行情 10s、LLM 60s，留出渲染时间。
- 退出时停止接收新更新，等待在途任务最多 15s。

## 5. 目录结构

```text
.
├── AGENTS.md
├── README.md
├── Makefile
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── go.mod
├── cmd/bot/main.go
├── internal/
│   ├── app/                 # 依赖装配、生命周期
│   ├── config/              # 环境变量解析与校验
│   ├── telegram/            # handler、命令、回调、发送
│   ├── agent/               # 分析管道、追问 Agent、提示词
│   ├── llm/                 # Provider 接口
│   │   └── openai/          # OpenAI Chat Completions 兼容适配器
│   ├── market/              # Symbol、Candle、Provider 接口、缓存
│   │   ├── binance/         # 主行情源
│   │   └── coingecko/       # 符号元数据/兜底
│   ├── analysis/            # 指标、支撑阻力、趋势评分
│   ├── metrics/             # Prometheus registry、业务指标
│   ├── session/             # 上下文预算、摘要、裁剪
│   ├── storage/             # repository 接口
│   │   └── sqlite/          # 实现 + migrations
│   ├── billing/             # Service 接口 + Noop
│   ├── trade/               # Executor 接口 + Disabled
│   ├── ratelimit/           # 令牌桶、并发控制
│   ├── render/              # HTML 转义、消息拆分、观点卡片
│   └── text/                # 中文文案常量
├── migrations/              # 嵌入二进制的 SQL
├── deploy/
│   └── monitoring/          # Grafana Alloy 采集配置
├── docs/
│   ├── PLAN.md
│   └── OPERATIONS.md
└── testdata/                # 行情/模型输出 fixture
```

## 6. 数据模型（SQLite）

```sql
users (
  id INTEGER PRIMARY KEY,
  telegram_user_id INTEGER NOT NULL UNIQUE,
  username TEXT,
  key_ciphertext BLOB,
  key_nonce BLOB,
  key_version INTEGER,
  key_updated_at DATETIME,
  key_status TEXT NOT NULL DEFAULT 'missing', -- missing/valid/invalid
  api_base_url TEXT NOT NULL DEFAULT 'https://api.openai.com/v1',
  api_base_url_updated_at DATETIME,
  model TEXT,
  mode TEXT NOT NULL DEFAULT 'swing',          -- short/swing
  status TEXT NOT NULL DEFAULT 'active',       -- active/banned
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)

sessions (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL UNIQUE REFERENCES users(id),
  chat_id INTEGER NOT NULL UNIQUE,
  current_symbol TEXT,
  current_market TEXT,                          -- crypto（当前固定）
  summary TEXT,
  summary_tokens INTEGER NOT NULL DEFAULT 0,
  last_analysis_json TEXT,                      -- 最近一次结构化观点
  generation INTEGER NOT NULL DEFAULT 0,        -- /reset 后递增，丢弃旧结果
  last_active_at DATETIME NOT NULL
)

messages (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL REFERENCES sessions(id),
  role TEXT NOT NULL,                           -- user/assistant
  content TEXT NOT NULL,
  tokens INTEGER NOT NULL,
  created_at DATETIME NOT NULL
)

usage_events (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL,
  request_id TEXT NOT NULL,
  kind TEXT NOT NULL,                           -- analysis/chat/summarize
  model TEXT,
  prompt_tokens INTEGER,
  completion_tokens INTEGER,
  estimated_cost_usd REAL,
  latency_ms INTEGER,
  status TEXT NOT NULL,                         -- ok/error
  created_at DATETIME NOT NULL
)

schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at DATETIME NOT NULL
)
```

约定：

- SQLite 开启 `WAL`、`foreign_keys=ON`、`busy_timeout=5000`、`synchronous=NORMAL`。
- MVP 写连接串行化（`MaxOpenConns(1)`），读也走同一连接池，避免 `SQLITE_BUSY`；单机足够。
- 迁移 SQL 用 `embed` 打进二进制，启动时按版本顺序执行；只做向前迁移。
- 消息默认保留 30 天，定时清理；`/reset` 立即删除该会话消息和摘要。
- `last_analysis_json` 只存结构化结果，不存原始 K 线。
- `api_base_url` 不是密钥，可明文存；但展示和日志里只显示域名，不显示完整路径/查询串。
- 不建充值/钱包表；等真正做 USDC 时再加迁移。

## 7. 用户 Key 安全

### 7.1 加密

- 主密钥 `APP_MASTER_KEY`：32 字节随机值的 base64，启动时校验长度。
- 算法：AES-256-GCM。
- 每条 Key 独立 12 字节 nonce；AAD 绑定 `user:<telegram_user_id>`，防止密文被挪用到其他用户。
- 存 `key_version`，为将来轮换主密钥留空间；解密失败时将该用户标记为 `invalid`，要求重新输入。
- 主密钥丢失无法恢复用户 Key，必须在部署文档中强调备份。

### 7.2 收取与删除

- `/key` 进入 pending 状态，内存保存 `user_id -> expiry`（5 分钟），不落库。
- 下一条私聊消息视为 Key；先调用 `deleteMessage` 删除用户消息，再校验。
- 删除失败（超过 48 小时、权限不足等）提示用户手动删除，并继续处理。
- 永远不回显完整 Key；日志、错误、`/status` 都只显示掩码。
- 校验失败不覆盖旧 Key；成功才原子更新。

### 7.3 日志脱敏

- 统一 `redact` 函数，至少过滤 `sk-[A-Za-z0-9_-]+`、Bot Token 形态、`Authorization` 头。
- 不记录用户消息正文；调试日志只记录长度、命令类型、符号。
- 用户/聊天 ID 在日志中用带盐哈希，保留排障能力但不直接暴露。

### 7.4 自定义 base_url 与 SSRF 防护

用户可配置任意 OpenAI 兼容地址，这是明确的 SSRF 入口，必须按不可信输入处理：

- 仅允许 `https://`；`ALLOW_INSECURE_BASE_URL=true` 时也只允许本地开发使用 `http://`。
- 本地联调 localhost 时，必须同时设置 `ALLOW_INSECURE_BASE_URL=true` 和 `ALLOW_PRIVATE_BASE_URL_FOR_DEV=true`；生产环境两者都保持 false。
- 规范化后必须包含路径（如 `https://api.example.com/v1`），禁止 URL userinfo、query、fragment；末尾 `/` 去掉。
- DNS 解析后校验所有 IP：拒绝回环、RFC1918 私网、链路本地、IPv6 ULA、组播、未指定地址，以及云元数据地址（如 `169.254.169.254`）。
- 在自定义 `DialContext` 里对实际连接的 IP 再校验一次，防止 DNS rebinding。
- `CheckRedirect` 一律拒绝重定向；`Proxy=nil`，不使用环境变量代理；连接/读取超时 10s；响应体上限 2MB。
- 可选 `CUSTOM_BASE_URL_ALLOWLIST` 域名白名单；公开上线建议配置，默认留空表示允许任意公网 HTTPS。
- 每个用户的 endpoint 独立建客户端，不复用连接池；只向该 endpoint 发送该用户自己的 Key。
- 代理不支持 `GET /models` 时，允许手动输入模型名，但必须先用一次最小 Chat Completions 请求校验可用性。
- 禁止把 Key 放在 URL 的 query 或 userinfo 中；日志只显示 endpoint 域名。

## 8. 行情数据层

### 8.1 统一模型

```go
type Symbol struct {
    Code   string // BTC
    Pair   string // BTCUSDT
    Market string // crypto（当前固定）
    Name   string // Bitcoin
}

type Candle struct {
    Open, High, Low, Close, Volume float64
    Time time.Time
}

type Snapshot struct {
    Symbol     Symbol
    Price      float64
    ChangePct  float64
    Timeframes map[string][]Candle
    FetchedAt  time.Time
    Source     string
    Partial    bool
}
```

### 8.2 代码解析

- 归一化：去空格、转大写、去掉 `$` 和 `/analyze` 前缀。
- 支持 `BTC`、`BTCUSDT`、`BTC/USDT`、`BTC-USD`；默认按 USDT 计价，统一映射为 Binance 交易对 `BTCUSDT`。
- 通过 Binance `exchangeInfo` 校验交易对（缓存 24h），只接受 `status=TRADING` 的现货 USDT 对。
- 排除杠杆代币（如 `BTCUP`、`BTCDOWN`）和非现货交易对，避免给出误导性观点。
- 非 Binance 币种：当前明确提示不支持，不调用 LLM。CoinGecko 兜底为低优先级，暂不实现。
- MVP 不需要股票/加密歧义处理；股票代码直接按“暂不支持”处理。

### 8.3 数据源与降级

| 用途 | 主源 | 备用 | 说明 |
| --- | --- | --- | --- |
| K 线/交易对 | `data-api.binance.vision` 的 `klines` / `exchangeInfo` | `api.binance.com`（若可达） | 当前 Oracle iad 上 `api.binance.com` 返回 451，vision 返回 200 |
| 价格/24h 涨跌 | `data-api.binance.vision` 的 `ticker/24hr` | CoinGecko `simple/price` | 缓存 15–30s |
| 符号元数据 | `data-api.binance.vision` 的 `exchangeInfo` | CoinGecko `coins/list` | 缓存 24h |
| 非 Binance 币种 | CoinGecko `coins/{id}/ohlc` | 无 | 粒度有限，标 `Partial` |

- Binance klines：15m/1h/4h/1d/1w；`limit=500`。
- CoinGecko 只做兜底，免费额度低；失败时宁可提示数据不可用，也不编造。
- 缓存：价格 15–30s，分钟线 60s，日线 10–15 分钟，符号元数据 24h；用 `singleflight` 合并并发请求。
- 重试：429/5xx 最多 2 次，指数退避 + 抖动；尊重 `Retry-After`；连续失败触发 30s 熔断（已实现于 `internal/resilience`）。
- 数据质量：
  - 指标至少需要 200 根 K 线；不足则标记 `Partial` 并减少指标。
  - 少于 50 根时拒绝给交易计划，只说明数据不足。
  - 行情时间戳必须展示；加密货币 24/7 交易，没有休市概念，但要说明数据延迟。
- 时区：统一 UTC；时间戳展示到分钟，不引入美股时区。
- 数据源条款：Binance/CoinGecko 的 ToS 和限流规则要在上线前确认；适配器隔离，后续可换付费源。

### 8.4 技术指标

在 Go 中确定性计算，**不让 LLM 算指标**：

- 趋势：EMA20/50/200、价格位置、EMA20 vs EMA50、近 20/50 根高低点结构。
- 动量：RSI14（Wilder）、MACD(12,26,9)、ROC10。
- 波动：ATR14、ATR%、布林带(20,2) 宽度。
- 趋势跟踪：SuperTrend（ATR 周期 10、乘数 3），输出线位、方向、与现价距离和距最近翻转的 K 线数。
- 量能：成交量 / 20 根均量。
- 支撑阻力：分形高低点（左右各 2 根）聚类，聚类阈值 `0.5 * ATR`，保留最近 3 个支撑/阻力。
- 多周期对齐：统计各周期趋势方向，作为 LLM 的参考字段，不直接生成买卖信号。

传给 LLM 的是“压缩后的指标 JSON”，不是原始 K 线：

```json
{
  "symbol": "BTC",
  "pair": "BTCUSDT",
  "market": "crypto",
  "mode": "swing",
  "as_of": "2026-11-02T20:30:00Z",
  "price": 68432.10,
  "change_pct": 1.23,
  "timeframes": {
    "1h": {"trend": "up", "ema20": 68010.5, "ema50": 67420.0, "rsi14": 58.2, "macd_hist": 42.0, "atr_pct": 1.1, "volume_ratio": 1.35, "support": [66800.0, 65200.0], "resistance": [70500.0, 72000.0]},
    "4h": {"trend": "up", "ema20": 67200.0, "ema50": 65800.0, "rsi14": 61.0, "macd_hist": 112.0, "atr_pct": 1.8, "volume_ratio": 1.1, "support": [65000.0, 63500.0], "resistance": [71000.0, 73000.0]}
  },
  "data_quality": "ok"
}
```

## 9. LLM 与 Agent 设计

### 9.1 Provider 接口

```go
type Provider interface {
    Complete(ctx context.Context, req Request) (Response, error)
}

type Request struct {
    Model       string
    System      string
    Messages    []Message
    Tools       []Tool
    JSONSchema  *Schema // 非 nil 时要求结构化输出
    MaxTokens   int
    Temperature float64
}
```

- 只走 Chat Completions：`POST {base_url}/chat/completions`，不依赖 Responses API，保证第三方兼容代理可用。
- `Provider` 不长期持有用户 Key；由 `llm.Factory` 按用户 `base_url` + 解密后的 Key 构建短期 client，调用结束即丢弃。
- OpenAI 实现负责把 `JSONSchema` 映射到 `response_format=json_schema`；代理/模型不支持时降级为 `json_object` + 本地校验，再不行则只返回文本并走降级渲染。
- 模型列表：优先 `GET {base_url}/models`；代理不支持时允许手动输入模型名，但必须先做一次最小 Chat Completions 校验。全局 `MODEL_ALLOWLIST` 仍然生效。
- 未来加厂商只需实现 `Provider`，agent 不感知 SDK。
- 追问已支持流式输出：`llm.Streamer` + OpenAI SSE 适配器，Telegram 以约 1.2s 间隔 `editMessageText` 增量更新；不支持流式时自动退回普通补全。
- 本地校验始终是最终兜底：即使走 JSON Schema，也要通过 `ParseOpinion` 的业务规则。

### 9.2 两条路径

1. **首次分析（确定性管道）**：先拉数据、算指标，再让 LLM 只做解释和结构化输出。`tool_choice=none`，减少延迟和不确定性。
2. **追问（有界 Agent）**：给 LLM 提供只读工具：
   - `get_market_snapshot(symbol, timeframes)`
   - `get_technical_analysis(symbol, timeframe, indicators)`
   - `resolve_symbol(query)`
   - 最多 `AGENT_MAX_TOOL_ROUNDS` 轮（默认 3）；超过则追加收口提示并用已有信息回答。
   - 同一轮内相同工具和参数命中 turn 内缓存，避免重复拉行情。
   - 不注册任何交易、账户、转账工具。
   - 代理不支持工具调用时自动降级为单轮文本回答。

### 9.3 结构化输出

```json
{
  "symbol": "BTC",
  "pair": "BTCUSDT",
  "market": "crypto",
  "mode": "swing",
  "direction": "bullish",
  "confidence": "medium",
  "summary": "一句话结论",
  "key_levels": {
    "support": [66800.0, 65200.0],
    "resistance": [70500.0, 72000.0]
  },
  "trade_plan": {
    "entry_low": 67800.0,
    "entry_high": 68600.0,
    "stop_loss": 65200.0,
    "take_profit": [70500.0, 72000.0],
    "risk_reward": 2.1
  },
  "reasons": ["...", "..."],
  "risks": ["...", "..."],
  "data_as_of": "2026-11-02T20:30:00Z",
  "disclaimer": "仅供参考，不构成投资建议。"
}
```

校验规则：

- 优先使用 `response_format=json_schema`（`trade_opinion`）；代理不支持时自动降级到 `json_object`，再失败仍由本地校验兜底。
- 枚举值合法；数字为正；多头止损 < 入场 < 目标，空头相反。
- `risk_reward` 与价格计算一致（允许 0.1 误差）。
- 中性观点允许 `trade_plan` 为空。
- 首次失败：带错误列表让模型修复一次；再失败则返回错误提示，不展示坏 JSON。
- 模型不得编造新闻/财报；提示词明确“只能使用给定数据，缺失就说缺失”。

### 9.4 提示词与成本

- 系统提示词中文，固定版本号，写入日志便于回溯。
- 明确：不保证收益、不推荐杠杆/满仓、不提供仓位大小（除非用户提供风险偏好）、必须给失效条件。
- `temperature=0.2`（`gpt-5`/o 系列推理模型不传 temperature），`max_completion_tokens=4000`（包含 reasoning tokens），上下文上限 6k tokens。
- 模型 allowlist 由 `MODEL_ALLOWLIST` 配置；`/model` 优先展示 endpoint 的 `/models`，代理用户可额外手动输入模型名。
- `STRICT_MODEL_LIST=true`（默认）时，模型必须出现在 endpoint 的 `/models` 返回列表中；仅当代理不支持 `/models` 时设为 false 以允许手动填写。
- 默认模型选便宜的小模型；调用前检查用户 Key 在该 endpoint 是否有权限，失败则回退默认模型或提示换模型。
- 自定义 `base_url` 可能不支持 `json_schema` 或工具调用；当前在单次调用内自动降级，暂不缓存能力探测结果，避免把临时错误误判为永久能力缺失。
- 记录 `prompt_version`、模型、token 用量、耗时，便于以后调优和计费。

### 9.5 可观测性与评测

- 每个 Telegram update 生成一个 `trace_id`，写入处理日志、工具调用日志、分析完成日志和 `usage_events.request_id`，可串联一次请求内的行情、LLM、摘要和重试。
- `internal/agent/testdata/golden_cases.json` 是固定行情 fixture + 固定模型输出的回归基线，覆盖单周期、三重滤网、中性无计划、非法交易计划等场景；`TestGoldenCases` 检查：
  - 分析请求始终携带 JSON Schema；
  - 报告包含正确的周期和 `as_of`；
  - prompt 引用了正确的策略、周期和 alignment；
  - 本地业务校验仍然拒绝非法交易计划；
  - 非冲突的三重滤网结论与观点方向一致。
- 安全边界、提示词注入防护、当前限制和 P1 待办见 `docs/SECURITY.md`。

## 10. 会话与上下文管理

> 私聊多会话/群组按用户隔离的详细设计见 `docs/SESSION_DESIGN.md`。
> 周期、指标和风险偏好的设计见 `docs/ANALYSIS_PROFILE_DESIGN.md`。

- 私聊：`user_id + chat_id` 下可有多条会话，由 `chat_state` 指向 active。
- 群组：`user_id + chat_id + scope='group'` 固定一条，不同成员互相隔离。
- 群组消息只处理 `@Bot` 提及、回复 Bot 和明确的命令，未提及消息不存储。
- 保存最近消息、会话摘要、当前标的、最近一次结构化观点。
- 预算：上下文使用超过 60% 时触发摘要；保留最近 8–10 轮 + 摘要 + 最近观点。
- 摘要用便宜模型，限 300 tokens；失败则直接裁剪最旧消息，不阻断主流程。
- 追问时注入：系统提示 + 摘要 + 最近观点 JSON + 最近消息 + 本次问题。
- `/reset` 清空消息和摘要并递增 `generation`；按产品决定，**不取消、不丢弃在途分析**，在途结果完成后仍写入会话。这是预期行为，不要擅自更改。
- 默认保留 30 天；定时任务删除过期消息。
- 如果摘要成本或复杂度失控，降级方案：只保留最近 10 轮，超限提示用户 `/reset`。

## 11. 限流、成本与滥用防护

公开可用意味着必须默认不信任用户：

- **强制自带 Key**：生产环境 `ALLOW_SERVER_KEY=false`，未配置 Key 不调用 LLM；服务端 Key 只用于本地开发/管理员。
- **自定义 endpoint 防护**：按用户限制出站请求频率；单次超时 10s、响应上限 2MB、禁止重定向；不允许把服务端变成任意 URL 代理。
- 每用户限流：分析 6 次/分钟、30 次/小时；追问 20 条/分钟；可配置。
- 每 chat 串行 + 队列上限 2；队列满直接拒绝并提示。
- 全局并发：LLM 16、行情 8；超出排队，排队超时返回“繁忙”。
- 单次请求上限：prompt 6k tokens、输出 1.2k tokens、工具循环 3 轮。
- 全局日预算：即使有人滥用，也有 `GLOBAL_DAILY_LLM_TOKENS` 硬上限；达到后暂停 LLM 分析并提示。
- Telegram 发送限速：每 chat 约 1 条/秒，全局 25 条/秒；429 按 `retry_after` 退避。已实现于 `RateLimitedSender`。
- 封禁：`users.status=banned`；MVP 可通过 `ADMIN_USER_IDS` + 管理命令或直接改库操作。
- 缓存：同一标的 + 模式 + 数据时间戳的 LLM 结果缓存 2–5 分钟，减少重复调用。
- 不记录用户 Key；限流按 Telegram user ID，不依赖 IP（long polling 拿不到）。

## 12. 计费与交易扩展点

### 12.1 计费（只留接口，不实现充值）

```go
type Service interface {
    Check(ctx context.Context, userID int64, feature string) (Decision, error)
    RecordUsage(ctx context.Context, usage Usage) error
    Balance(ctx context.Context, userID int64) (Balance, error)
}
```

- MVP 用 `NoopService`：永远允许，但 `RecordUsage` 真实写入 `usage_events`。
- agent 在调用 LLM 前统一走 `Check`，以后接配额/订阅不用改业务代码。
- 未来 USDC 充值需要：充值地址生成、链上监听、确认数、幂等账本、退款/对账、风控；这些都不在 MVP 建表。
- 不在客户端展示价格/套餐，避免提前承诺。

### 12.2 交易（永远默认关闭）

```go
type Executor interface {
    PlaceOrder(ctx context.Context, order Order) (Result, error)
    Cancel(ctx context.Context, orderID string) error
    Positions(ctx context.Context) ([]Position, error)
}
```

- MVP 只有 `DisabledExecutor`，返回 `ErrTradingDisabled`。
- 不把交易方法注册成 LLM 工具；即使未来接入，也必须显式开关 + 用户二次确认 + 单笔限额 + kill switch。
- 不引入 Binance/Gate SDK，等真正做执行时再加。
- 未来顺序：只读持仓/行情 → 模拟盘 → 小额实盘；A 股始终不做。

## 13. 部署与运维

### 13.1 配置

`.env.example` 至少包含：

```dotenv
APP_ENV=prod               # prod/staging
INSTANCE_NAME=guanlanai-prod
TELEGRAM_BOT_TOKEN=        # 生产 bot；staging 用测试 bot
APP_MASTER_KEY=            # openssl rand -base64 32；两套环境必须不同
DATABASE_PATH=/data/guanlanai.db
OPENAI_BASE_URL=https://api.openai.com/v1   # 全局默认；用户可覆盖
OPENAI_API_KEY=            # 仅本地开发；生产默认禁用
BINANCE_BASE_URL=https://data-api.binance.vision
BINANCE_FALLBACK_BASE_URL=https://api.binance.com
COINGECKO_BASE_URL=https://api.coingecko.com/api/v3
ALLOW_SERVER_KEY=false
ALLOW_CUSTOM_BASE_URL=true
CUSTOM_BASE_URL_ALLOWLIST= # 留空=任意公网 HTTPS；仍做 SSRF 防护
ALLOW_INSECURE_BASE_URL=false
ALLOW_PRIVATE_BASE_URL_FOR_DEV=false
CUSTOM_BASE_URL_TIMEOUT=10s
CUSTOM_BASE_URL_MAX_RESPONSE_BYTES=2097152
DEFAULT_MODEL=
MODEL_ALLOWLIST=
STRICT_MODEL_LIST=true
DEFAULT_MODE=swing
LOG_LEVEL=info
HTTP_ADDR=:8080
METRICS_ENABLED=true
# 可选：Grafana Cloud Free + Alloy 采集
GRAFANA_CLOUD_REMOTE_WRITE_URL=
GRAFANA_CLOUD_METRICS_USERNAME=
GRAFANA_CLOUD_API_KEY=
DATA_RETENTION_DAYS=30
MAX_PRIVATE_SESSIONS=20
GROUPS_ENABLED=false
ANALYSIS_RATE_PER_MIN=6
ANALYSIS_RATE_PER_HOUR=30
CHAT_RATE_PER_MIN=20
TELEGRAM_MAX_CONCURRENCY=32
LLM_MAX_CONCURRENCY=16
MARKET_MAX_CONCURRENCY=8
MAX_PROMPT_TOKENS=6000
MAX_OUTPUT_TOKENS=4000
GLOBAL_DAILY_LLM_TOKENS=2000000
AGENT_MAX_TOOL_ROUNDS=3
LLM_STREAMING=true
TRADING_ENABLED=false
```

启动时校验必填项和格式，失败直接退出并打印缺失项（不打印值）。

- `OPENAI_BASE_URL` 只是新用户/未配置用户的默认值；每个用户的覆盖值存 `users.api_base_url`。
- `CUSTOM_BASE_URL_ALLOWLIST` 非空时，只允许这些域名（含子域名按配置规则）；公开上线建议配置。
- 用户未配置 Key 时不允许访问任何 LLM endpoint；`/baseurl` 本身不触发外部请求。

### 13.2 Docker 与甲骨文 VPS

- 多阶段构建：`golang:1.25-alpine` 编译，`CGO_ENABLED=0`，`-trimpath`，ldflags 注入版本；Dockerfile 使用 `TARGETOS/TARGETARCH`，支持 `linux/amd64` 和 `linux/arm64`。
- 运行镜像用 `alpine` + `ca-certificates`（或 distroless static），非 root 用户；时间统一 UTC，不依赖美股时区。
- `/data` 挂 volume；SQLite 文件和备份都在这里。
- `docker-compose.yml`：`env_file: .env`、`restart: unless-stopped`、healthcheck 调 `/healthz`、日志 `max-size` 限制。
- 迁移在启动时自动执行；升级前先备份。
- 优雅退出：SIGTERM 后停止 polling，等待在途任务，关闭 DB。

甲骨文免费 VPS 注意事项：

- long polling 只需要出站 443，不需要入站 HTTP；Security List/NSG 只放 SSH（最好限制来源 IP），不要把 8080 暴露到公网。
- 同时检查主机防火墙（firewalld/ufw/iptables）；Oracle 镜像的默认规则会和 Security List 叠加，排查连不上时两边都看。
- 先确认架构：`uname -m`。Ampere 机型是 `aarch64`，必须构建 `linux/arm64` 镜像或多架构镜像，不能在 x86 上构建后直接拷过去。
- 免费 AMD 小机型内存可能不足以在 VPS 上编译 Go 项目；优先本地/CI 构建后推镜像，或在 VPS 加 swap，避免 OOM。
- 区域要能访问 `data-api.binance.vision` 和 `api.openai.com`；`api.binance.com` 在部分地区会返回 451。部署前在 VPS 上 `curl` 验证。
- 保留公网 IP，确认 NTP 时间同步；注意免费额度、块存储和出站流量。
- 单 VPS 是单点故障：生产/测试会一起挂；备份至少要定期拉到 VPS 外。

当前 VPS 实测（2026-11-02）：

- Oracle `VM.Standard.A1.Flex`，region `iad`（US-Ashburn），Ubuntu 26.04，`aarch64`，1 OCPU，5.8GB RAM，45GB 磁盘（41GB 可用），无 swap。
- Docker 29.1.3 / Compose 2.40.3 已安装，`ubuntu` 已加入 `docker` 组；镜像 `guanlanai:phase0`、`phase1`、`phase2` 已加载。
- `guanlanai-prod-bot-1`、`guanlanai-staging-bot-1` 均 healthy，均已切 `metrics-v1`；`guanlanai-prod-alloy-1`、`guanlanai-staging-alloy-1` 运行中；容器用户 `PUID/PGID=1001`，无宿主端口发布，SQLite 文件权限 0600。
- `api.binance.com` 返回 451，`data-api.binance.vision` 返回 200，CoinGecko 200，OpenAI 401（网络可达）。
- 22 端口用于 SSH；111（rpcbind）在监听，若无 NFS 需求建议 `systemctl disable --now rpcbind`，并确认 Security List 未放行。
- 1 OCPU 上编译 Go 会很慢；优先本机/CI 构建 `linux/arm64` 镜像后推送或传输，避免在 VPS 上反复编译。

双环境（生产 + 测试 bot）：

```text
/opt/guanlanai/
├── prod/
│   ├── .env
│   ├── docker-compose.yml
│   └── data/
└── staging/
    ├── .env
    ├── docker-compose.yml
    └── data/
```

- 两套环境使用不同 Bot Token、不同 `APP_MASTER_KEY`、不同 SQLite、不同备份目录；绝不共用。
- compose 设置 `name: guanlanai-prod` / `name: guanlanai-staging`，避免容器、网络、volume 重名。
- 默认不发布端口；需要主机侧健康检查时，分别绑 `127.0.0.1:8080` 和 `127.0.0.1:8081`。
- 同一个镜像 tag 先部署 staging，冒烟通过后再部署 prod；生产升级前备份。
- 给 staging 设置较低的 CPU/内存和更严格的限流；小机型同时跑两套要观察 OOM 和磁盘。
- 测试 bot 建议只让你的测试账号使用；生产仍按公开访问设计。

### 13.3 备份与恢复

- 提供 `guanlanai backup` 子命令，用 `VACUUM INTO` 生成一致性备份到 `/data/backups`。
- 每日 cron/宿主机 timer 执行；保留 7–14 天；生产/测试分目录。
- 定期把备份同步到 VPS 外（OCI Object Storage、另一台机器或本地），避免 VPS 整机故障丢两套环境。
- 恢复流程写入 `docs/OPERATIONS.md`，上线前做一次演练。
- 主密钥单独备份；丢失后用户需重新输入 Key。生产/测试的主密钥分别备份，不要混用。

### 13.4 可观测性

- `slog` JSON 日志到 stdout；字段：request_id、update_id、用户/chat 哈希、命令、symbol、provider、耗时、token、状态。
- 不记录消息正文和密钥。
- `/healthz`：DB ping + 进程存活；不每次请求都打外部 API。
- `/metrics`（已实现，`METRICS_ENABLED=true`）：Prometheus 格式，与健康检查共用内部 HTTP 端口，默认不发布到宿主。
  - `guanlanai_requests_total{kind,status}`：分析、追问、摘要请求数和错误率。
  - `guanlanai_request_duration_seconds{kind}`：请求延迟直方图。
  - `guanlanai_requests_in_flight{kind}`：正在处理的请求数。
  - `guanlanai_llm_calls_total{operation,status}`、`guanlanai_llm_call_duration_seconds{operation}`：LLM 调用次数和延迟。
  - `guanlanai_llm_tokens_total{operation,type}`：prompt/completion token 用量。
  - `guanlanai_rate_limit_hits_total{kind}`：按用户限流命中。
  - `guanlanai_telegram_updates_total{result}`、`guanlanai_telegram_messages_total{status}`：Telegram 更新和发送结果。
  - `guanlanai_market_calls_total{operation,status}`、`guanlanai_market_call_duration_seconds{operation}`：行情调用次数和延迟。
  - `guanlanai_build_info{instance,env,version,commit}`：实例和版本信息；另含 Go runtime/process 指标。
  - 标签只使用有界值，不包含用户 ID、chat ID、币种或模型名，避免高基数。
- 采集（可选）：`deploy/monitoring/alloy.alloy` + Compose `monitoring` profile；Alloy 在 VPS 本地抓取 `/metrics`，通过出站 remote_write 推送到 Grafana Cloud Free（10k series、14 天保留），不开放入站端口。
- 错误信息对用户友好，内部错误带 request_id 方便排查。

## 14. 测试与 CI

### 14.1 测试重点

- 代码解析：`BTC`、`ETH`、`BTCUSDT`、`btc/usdt`、`BTC-USD`、非 Binance 币种、杠杆代币、非法输入。
- 指标：用固定 fixture 对比已知值（RSI/MACD/ATR）；纯函数、无网络。
- 行情适配器：`httptest` + 录制 JSON；覆盖 Binance 429/5xx、缺字段、空 K 线、部分数据、CoinGecko 兜底。
- 加密：加密/解密、错误主密钥、篡改密文、AAD 不匹配、Key 轮换。
- 自定义 base_url：规范化、拒绝 http/私网/回环/链路本地/云元数据、DNS rebinding、重定向、超时、响应大小、白名单。
- 会话：裁剪、摘要触发、`/reset`、过期清理、generation 丢弃旧结果。
- LLM：fake provider；结构化输出校验、修复重试、无效 Key、超时、限流；代理不支持 `json_schema`/`/models` 时的降级。
- Telegram handler：fake sender；验证 Key 消息删除、pending 过期、命令路由、回调数据。
- 集成：临时 SQLite + 假行情/假 LLM，跑完整“输入代码 → 输出卡片”。
- `go test -race ./...` 必须通过。
- 可选 live smoke：build tag `smoke`，手动跑真实 Binance/CoinGecko/OpenAI，不进 CI。

### 14.2 Makefile 与 CI

```make
fmt:      gofmt -w .
vet:      go vet ./...
test:     go test ./...
test-race: go test -race ./...
lint:     go vet ./... && golangci-lint run
build:    CGO_ENABLED=0 go build -trimpath -o bin/guanlanai ./cmd/bot
run:      go run ./cmd/bot
docker:   docker build -t guanlanai:$(shell git rev-parse --short HEAD) .
docker-multi: docker buildx build --platform linux/amd64,linux/arm64 -t guanlanai:$(shell git rev-parse --short HEAD) .
```

- 若仓库支持 GitHub Actions：push/PR 跑 fmt 检查、vet、lint、test-race、build；tag 构建多架构镜像并推 GHCR。
- 若当前 `xyths.github.com` 不是标准 GitHub Actions 环境，先保留 Makefile，CI 后补。
- 没有镜像仓库时，在 VPS 上按本机架构构建一次，prod/staging 复用同一个镜像 tag。
- 部署顺序：staging 先上，用测试 bot 冒烟，再切 prod；镜像 tag 用 git SHA，便于回滚。
- 不把真实 Key 放进 CI；测试全部用 fake。

## 15. 里程碑与验收

> 估算按 1 名熟悉 Go 的开发者、每天有效 6 小时计，不含数据源商务谈判和大规模合规工作。

### Phase 0：骨架（已完成，2026-11-02）

- `go mod init`、目录、配置加载、slog、Makefile、`.env.example`、多架构 Dockerfile、prod/staging compose 模板、`/healthz`、迁移框架。
- 验收：`make build` 成功；本机架构镜像可运行；`docker compose up` 健康；缺少必填配置时启动失败且提示清晰。
- 实际状态：Oracle VPS 上 prod/staging 两个容器均已 healthy；Phase 0 镜像为 `guanlanai:phase0`；SQLite 迁移已执行；健康检查只走容器内。

### Phase 1：Telegram 骨架（已完成，2026-11-02）

- long polling、私聊过滤、per-chat 串行队列、全局并发上限、HTML 转义/拆分、命令路由、优雅退出。
- `/start`、`/help`、`/status`、`/reset`；`/key`、`/baseurl`、`/model`、`/mode` 先返回阶段提示。
- 验收：Bot 能稳定收发；长消息不超限；重启不 panic；同一 chat 消息顺序正确。
- 实际状态：staging `@ax_ha_1_bot` 与 prod `@GuanLanAI_bot` 均已连接并 healthy；已用 `/start`、`BTC`、`/status` 验证收发链路。

### Phase 2：用户与 Key（已完成，2026-11-02）

- SQLite repository、用户/会话表、Key 加密、`/key` 收取与删除、Key 校验、`/baseurl`、`/model`、`/status` 掩码、自定义 endpoint 的 SSRF 防护。
- 验收：Key 不以明文出现在 DB/日志；删除消息成功；错误 Key 有明确提示；自定义代理可用；私网/元数据地址被拒绝；重启后 Key 仍可用。
- 实际状态：`internal/secrets`、`internal/netguard`、`internal/llm/openai`、`internal/user`、用户 SQLite repository 和 `/key`/`/baseurl`/`/model`/`/mode`/`/status` 已实现。staging 与 prod 均已用真实 Key 验证：Key 为 BLOB 密文、无 `sk-` 明文、`key_status=valid`；`/model` 与 `/mode` 已生效。

### Phase 3：行情与观点（已实现，staging 验证通过，prod 已部署）

- 代码解析、Binance 行情适配器、缓存/重试、指标、支撑阻力、结构化提示词、JSON 校验/修复、观点卡片。
- 验收：`BTC`、`ETH`、`SOL`、`BNB` 均能输出完整观点；数据时间戳正确；非 Binance 币种有明确提示；行情失败不编造。
- 实际状态：staging 已用 `gpt-5-mini` 对 BTC/SOL/BNB 完成分析，耗时约 17–23 秒，无错误；`MAX_OUTPUT_TOKENS=4000` 避免 reasoning tokens 截断。熔断器留到 P1；CoinGecko 兜底为低优先级，暂不实现。

### Phase 4：会话与限流（已完成，2026-11-02）

- 追问 Agent、上下文预算、摘要、`/reset`、每用户限流、全局并发、usage 记录、错误 UX。
- 验收：追问能引用上一次观点；超限有提示；`/reset` 后不串上下文；并发压测不崩。
- 实际状态：staging 已验证分析→追问→`/status`→`/reset`→无上下文追问的完整流程；usage 记录 `analysis` 1 次、`chat` 2 次，无错误。生产已切 `phase5`，`/reset` 会清空会话和摘要。

### Phase 5：加固与上线（已完成，2026-11-02）

- 全量测试、race、安全自查（重点：自定义 `base_url` 的 SSRF、Key 泄漏）、备份/恢复、部署文档、真实环境 smoke、日志脱敏复查。
- 甲骨文 VPS 上线：确认架构与镜像匹配、Security List/NSG 只开 SSH、VPS 能访问 Binance/OpenAI、NTP 正常；先部署 staging bot 冒烟，再切 prod；备份同步到 VPS 外。
- 验收：`go test -race ./...` 通过；Docker 重启后数据完整；备份可恢复；无交易入口；私网/元数据地址不可达；staging 与 prod 互不影响。
- 实际状态：新增 `guanlanai backup`（`VACUUM INTO`）并已在 staging 生成可读取的备份；观点 summary/reasons/risks 增加长度上限，避免 Telegram 4096 截断；备份、恢复、回滚写入 `docs/OPERATIONS.md`。P1 补丁已实现 Telegram 发送限速/429 和 Binance/OpenAI 熔断器；CoinGecko 兜底为低优先级，暂不实现。

**总计约 10–15 个开发日（2–3 周）**，建议预留 20% buffer。相比股票+加密双市场，加密单市场大约省 1–2 天。

## 16. 上线验收清单

> 实际验收结果和偏差记录见 `docs/ACCEPTANCE.md`。

- [x] `/start → /key → BTC` 全流程可用；首次观点实测 17–23s，已确认接受（原 `<10s` 目标不再适用）。
- [ ] 支持 Binance USDT 现货主流加密货币；非法/非支持代码有友好提示。
- [ ] 股票代码明确提示“暂不支持股票”，不误判为加密货币。
- [ ] Key 加密存储；消息删除；日志无明文；`/status` 只显示掩码。
- [ ] 无 Key 时不能分析；服务端 Key 在生产默认禁用。
- [ ] 自定义 `base_url` 可用（OpenAI 兼容代理）；http/私网/回环/云元数据/重定向被拒绝；响应大小和超时受限。
- [ ] 私聊和群组可用；群组按成员隔离；用户消息默认 30 天清理；`/reset` 立即删除。
- [ ] 结构化输出通过校验；失败有降级，不展示坏 JSON。
- [ ] 每次观点都有数据时间、来源、风险提示、失效条件。
- [ ] 追问能继承标的和最近观点；`/reset` 彻底清空。
- [ ] 限流、并发上限、超时、重试、熔断生效。
- [ ] Oracle VPS（arm64）安装 Docker/Compose；镜像架构匹配；`docker compose up` 健康；重启数据不丢。
- [ ] Security List/NSG 只开放 SSH，8080 不暴露公网；rpcbind 111 未对外暴露或已禁用。
- [ ] VPS 可访问 `data-api.binance.vision`、CoinGecko、OpenAI；已知 `api.binance.com` 在该区域返回 451，代码默认走 vision。
- [ ] staging 与 prod 使用不同 Bot Token、主密钥、SQLite、volume、compose project；staging 冒烟通过后再升级 prod。
- [ ] 备份/恢复演练通过；至少一份备份在 VPS 外。
- [ ] 代码中不存在真实下单路径；`TRADING_ENABLED=false`。
- [ ] `gofmt`、`go vet`、`go test -race ./...` 全绿。

## 17. 风险与应对

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| Binance 地区限制/接口变更 | 加密行情不可用 | 部署前在 VPS 上验证可达性；`data-api.binance.vision` + 备用地址，适配器隔离；CoinGecko 低优先级 |
| Oracle 区域限制 OpenAI/Binance | 核心依赖不可用 | 当前 iad 实测 OpenAI 可达、`api.binance.com` 451；默认 vision，部署前复测 |
| VPS 架构与镜像不匹配 | 容器无法启动 | 构建 amd64/arm64 多架构镜像，或按 `uname -m` 在 VPS 构建 |
| 单 VPS 故障/免费额度限制 | 生产+测试同时不可用 | 离线备份、恢复演练、资源限制和监控；接受单点风险 |
| Security List 误开放 8080 | 健康检查/内部接口暴露 | 默认不发布端口，只开 SSH，定期检查安全列表 |
| 免费源限流/延迟 | 体验差、观点过时 | 缓存、singleflight、退避、展示数据时间 |
| 加密市场 24/7 剧烈波动 | 观点快速失效、滑点 | 展示数据时间、ATR/波动提示、明确失效条件 |
| 小币种流动性差/被操纵 | 错误信号 | 优先主流币种，检查成交量/流动性，数据不足时拒绝给计划 |
| LLM 幻觉或过度自信 | 错误交易观点 | 指标确定性计算、严格 schema、只给有限数据、风险提示、置信度 |
| 用户 Key 泄露 | 用户账户损失 | 加密、删除消息、日志脱敏、最小化内存持有、主密钥备份 |
| 自定义 base_url 触发 SSRF | 访问内网/云元数据 | 仅 HTTPS、IP 校验、DialContext 二次校验、禁重定向、超时/响应限制、可选白名单 |
| 公开访问被滥用 | 服务被打爆/成本失控 | 强制自带 Key、每用户限流、全局并发/日预算、封禁 |
| SQLite 损坏/丢失 | 用户配置和会话丢失 | WAL、每日备份、恢复演练、volume 持久化 |
| Telegram 限流 | 消息丢失/延迟 | 发送限速、429 退避、长消息拆分 |
| 模型下线/不支持结构化输出 | 分析失败 | 模型 allowlist 可配置、启动/调用时校验、默认模型回退 |
| 合规风险 | 被认定为投资建议 | 按当前决定暂不专门做合规审查；保留显著免责声明，公开大规模推广前再评估 |
| 主密钥丢失 | 用户 Key 无法解密 | 部署文档强制备份；丢失后要求用户重新输入 |

## 18. 暂缓与未来路线

- **P1（MVP 后）**：流式回复、更多 OpenAI 兼容厂商、自选列表/定时早报、新闻摘要、管理后台；Prometheus 指标已实现（`internal/metrics`，`/metrics`）。
- **P2**：USDC 充值（链选择、充值地址、链上监听、账本、配额）、订阅/次数包、推荐返利。
- **P3**：交易所只读持仓 → 模拟盘 → 小额实盘；优先 Binance/Gate；真实下单必须二次确认、限额、kill switch。
- **P4**：回测、策略对比、组合分析；仍不接 A 股。
- **股票后置**：等加密 MVP 稳定后，再评估美股/ETF；行情源和代码解析作为独立适配器接入，不改变 agent 核心。

## 19. 默认假设（可调整）

- 部署在一台甲骨文免费 VPS；long polling 不开放入站 HTTP；测试 bot 可选同机部署，按本文双环境规则隔离。
- 默认构建 amd64/arm64 多架构镜像；上线前用 `uname -m` 确认 VPS 架构。
- 加密货币 24/7 交易，统一 UTC 展示时间；MVP 以 Binance USDT 现货为主，非 Binance 币种只做有限兜底。
- 暂不支持股票代码，用户发股票代码时明确提示“当前仅支持加密货币”。
- 不提供仓位大小建议；只给入场/止损/目标和风险框架。
- 自定义 `base_url` 默认允许任意公网 HTTPS，并强制 SSRF 防护；如要收紧，配置 `CUSTOM_BASE_URL_ALLOWLIST`。
- 代理用户可手动输入模型名，但必须先通过该 endpoint 的最小 Chat Completions 校验。
- 模型 allowlist 在部署时确定，不在代码里写死具体型号。
- 免费行情源只用于 MVP；上线前确认 ToS 和稳定性。
- 暂不专门做合规审查；保留免责声明和风险提示。

## 20. 需要你后续拍板的问题

已确认：支持私聊多会话和群组按成员隔离；支持每用户 OpenAI 兼容 `base_url`；消息保留 30 天；暂不专门做合规审查；VPS 为 Oracle A1.Flex `arm64`、region `iad`，Docker 待安装。

剩余问题（不阻塞 Phase 0–2，可在对应阶段前确认）：

1. 模型 allowlist 的具体名单和默认模型，等实现时按你的 OpenAI 账户可用模型确定。
2. 未来 USDC 充值优先哪条链（EVM/TRON/Solana），以及是否自托管钱包；现在只影响接口命名，不阻塞 MVP。
3. 未来交易所优先级：Binance 还是 Gate 先接？只读持仓是否比下单更早做？
