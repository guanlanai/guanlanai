# 架构与流程

本文用 Mermaid 说明观澜当前的运行时结构、两条 LLM 路径和部署拓扑。图直接写在 Markdown 里，方便随代码迭代修改。设计细节和里程碑见 [PLAN.md](PLAN.md)，安全边界见 [SECURITY.md](SECURITY.md)。

## 1. 运行时架构

```mermaid
flowchart TB
    subgraph CLIENT["接入"]
        direction LR
        U["Telegram 私聊 / 群组<br/>@提及 · 回复 · 命令"]
        TG["Telegram Bot API<br/>long polling · send / edit / delete"]
    end

    subgraph EDGE["接入处理 · internal/telegram"]
        direction LR
        D["Dispatcher<br/>每 chat 串行 · 全局并发"]
        R["命令路由<br/>命令优先 + 意图识别"]
        L["速率限制<br/>分析 / 追问 / 发送"]
        S["Sender / Editor<br/>HTML 转义 · 流式编辑"]
    end

    subgraph AGENT["Agent 编排 · internal/agent"]
        direction LR
        A["Analyze<br/>行情 → 指标 → 观点"]
        C["Chat<br/>≤3 轮只读工具 · turn 缓存"]
        SM["Summarize<br/>摘要 + 裁剪"]
        G["Guardrails<br/>注入防护 · token 预算"]
    end

    subgraph DOMAIN["领域与端口"]
        direction LR
        AN["analysis<br/>EMA / RSI / MACD / ATR / SuperTrend"]
        MK["market<br/>Resolver / Provider / 快照缓存"]
        LL["llm<br/>Completer / Streamer / Schema"]
        TL["tools<br/>resolve · snapshot · technical"]
    end

    subgraph STATE["状态与可观测性"]
        direction LR
        DB["SQLite<br/>users · sessions · messages · usage"]
        SEC["Secrets<br/>AES-256-GCM"]
        OBS["Observability<br/>trace_id · 日志脱敏"]
        H["Health<br/>/healthz · /readyz"]
    end

    CLIENT -->|update| EDGE
    EDGE -->|命令 / 查询| AGENT
    AGENT -->|指标 / 观点| DOMAIN
    AGENT -->|读写会话| STATE
    DOMAIN --> BIN["Binance<br/>USDT 现货"]
    DOMAIN --> OAI["OpenAI 兼容 endpoint<br/>Chat Completions"]
```

分层原则：

- **接入层**只负责 Telegram update、命令路由、限流、发送和流式编辑，不直接依赖行情或 LLM 实现。
- **Agent 编排层**负责分析管道、追问工具循环、摘要和护栏；它依赖 `market`、`llm`、`analysis`、`session` 的接口。
- **领域与端口层**把确定性计算和外部适配器分开：`analysis` 是纯函数，`market` 和 `llm` 是端口，具体实现放在 `market/binance`、`llm/openai`。
- **状态与可观测性**统一走 SQLite、AES-256-GCM、`trace_id`、日志脱敏和健康检查。

## 2. 首次分析流程

```mermaid
flowchart TD
    A["1. 用户发送 BTC / /analyze BTC"] --> B["2. 命令与意图识别"]
    B --> C["3. 限流 + Key / 模型 / base_url 校验"]
    C --> D["4. 捕获 strategy / timeframe / indicators / sessionID"]
    D --> E["5. Resolve Symbol"]
    E --> F{"SnapshotCache 命中？"}
    F -- 是 --> G["复用会话快照"]
    F -- 否 --> H["Binance 多周期 Snapshot"]
    G --> I["6. BuildStrategyReport<br/>EMA / RSI / MACD / ATR / SuperTrend<br/>三重滤网计算 alignment"]
    H --> I
    I --> J["7. 组装 &lt;market_data&gt; + JSON Schema"]
    J --> K["8. LLM 结构化观点<br/>json_schema → json_object 降级"]
    K --> L{"ParseOpinion 校验通过？"}
    L -- 是 --> N["9. OpinionCard + 私聊仓位"]
    L -- 否 --> M{"已修复过一次？"}
    M -- 否 --> R["带错误提示重试一次"]
    R --> K
    M -- 是 --> Q["返回错误，不展示坏 JSON"]
    N --> O["10. 保存 last_analysis / messages / usage / trace"]
    O --> P["发送 Telegram"]

    classDef det fill:#ECFDF5,stroke:#10B981,color:#065F46
    classDef llm fill:#F5F3FF,stroke:#8B5CF6,color:#5B21B6
    classDef out fill:#EFF6FF,stroke:#3B82F6,color:#1E3A8A
    classDef fail fill:#FFFBEB,stroke:#F59E0B,color:#92400E
    class E,F,G,H,I det
    class J,K,L,M,R llm
    class N,O,P out
    class Q fail
```

关键点：

1. 先做命令/意图识别、限流、Key 和模型校验。
2. 在发起分析时捕获 `strategy`、`timeframe`、`indicators` 和 `sessionID`；在途结果仍写回发起时的会话。
3. 行情、指标、支撑阻力和三重滤网 `alignment` 全部由代码计算，不让 LLM 做数学。
4. LLM 优先使用 `json_schema`，不支持时自动降级 `json_object`。
5. `ParseOpinion` 始终做本地业务校验；失败修复一次，再失败返回错误而不是展示坏 JSON。
6. 结果写入 `LastAnalysisJSON`、消息、usage 和 trace，然后渲染成 Telegram 卡片。

## 3. 追问与工具循环

```mermaid
flowchart TD
    A["1. 用户追问"] --> B["2. 会话与输入检查<br/>LastAnalysisJSON · ≤2000 字符 · 限流"]
    B --> C["3. RecentMessages(20)"]
    C --> D{"超过 12 条或 >3000 tokens？"}
    D -- 是 --> E["Summarize + TrimMessages(8)"]
    D -- 否 --> F["5. 组装 &lt;context&gt;<br/>摘要 + 最近分析 + 最近消息"]
    E --> F
    F --> G["6. 调用 LLM（带只读工具）"]
    G --> H{"有 tool_calls？"}
    H -- 是 --> I["执行只读工具<br/>单轮 ≤4 · 单次 ≤8 · turn 缓存<br/>结果包在 &lt;tool_result&gt;"]
    I --> G
    H -- 否 --> J["8. 最终回答"]
    J --> K["9. 流式 editMessageText<br/>1.2s 节流"]
    K --> L["10. 保存消息 / usage / trace"]
    G -. 工具不支持 .-> M["降级为无工具文本回答"]
    M --> J
    K -. 首个内容前失败 .-> N["非流式补全 + 普通发送"]
    N --> L
    B -. 超预算 .-> O["拒绝并提示 /reset"]
```

关键点：

- 只有当前会话存在 `LastAnalysisJSON` 时才会进入追问；否则返回固定提示。
- 上下文由“摘要 + 最近一次分析 + 最近消息”组成，超过预算时先压缩再裁剪。
- 追问默认最多 3 轮只读工具；单轮最多执行 4 个、单次最多 8 个，相同参数命中 turn 内缓存。
- 工具结果包在 `<tool_result>` 中，工具错误以 JSON 回传，不中断回答。
- 工具不支持时降级为无工具文本回答；流式在首个内容前失败时退回普通补全。
- 流式输出按约 1.2 秒节流编辑 Telegram 占位消息，编辑失败不中断生成。

## 4. 部署拓扑

```mermaid
flowchart LR
    OPS["运维边界<br/>Security List/NSG 只开 SSH<br/>不暴露 8080<br/>容器 1001:1001 · read_only"]

    subgraph VPS["Oracle Cloud VPS · Ubuntu 26.04 · aarch64"]
        direction TB
        subgraph PROD["prod · guanlanai-prod"]
            direction TB
            P1["guanlanai-prod-bot-1<br/>image: guanlanai:IMAGE_TAG · 512m / 0.75 CPU"]
            P2[".env · Bot Token<br/>./data:/data<br/>/healthz · /readyz"]
        end
        subgraph STAGING["staging · guanlanai-staging"]
            direction TB
            S1["guanlanai-staging-bot-1<br/>image: guanlanai:IMAGE_TAG · 256m / 0.25 CPU"]
            S2["独立 .env / Key<br/>独立 ./data<br/>/healthz · /readyz"]
        end
    end

    BAK["VPS 本地 cron · 每日 03:15<br/>backup.sh → data/backups<br/>保留 14 天"]
    TG["Telegram Bot API<br/>long polling"]
    BIN["Binance<br/>data-api.binance.vision"]
    OAI["OpenAI 兼容 endpoint<br/>用户自带 Key / base_url"]

    OPS -.-> VPS
    VPS --> TG
    VPS --> BIN
    VPS --> OAI
    VPS --> BAK
```

- 单台 Oracle Cloud VPS（`aarch64`），prod/staging 各自一个 Docker Compose project；镜像 tag 由部署命令的 `IMAGE` 注入，图中用 `IMAGE_TAG` 表示。
- 两套环境使用独立 `.env`、Bot Token、`APP_MASTER_KEY`、SQLite 和备份目录。
- 容器以 `1001:1001` 运行，根文件系统只读，`/tmp` 使用 tmpfs，不发布 8080。
- Security List/NSG 只开放 SSH；健康检查在容器内访问 `127.0.0.1:8080`。
- 每日 `03:15` 执行 `backup.sh`，对 prod/staging 分别 `VACUUM INTO`，保留 14 天。

## 5. 关键设计取舍

| 取舍 | 选择 | 原因 |
|---|---|---|
| 首次分析 | 确定性管道 + LLM 解释 | 指标可复现、可测试，降低幻觉 |
| 追问 | 有界工具循环 | 允许补拉数据，但限制轮数和调用量 |
| 结构化输出 | JSON Schema + 本地校验 | Schema 约束形状，本地校验约束业务不变量 |
| 工具 | 只读、白名单 | 不暴露交易/账户/文件/任意 URL |
| 会话 | SQLite 持久化 + 按 user/chat 隔离 | 支持多会话、群组隔离和 30 天保留 |
| 流式 | 追问可流式，分析非流式 | 分析输出是 JSON，直接流式展示意义不大 |
| 可观测性 | `trace_id` + 日志脱敏 | 串联一次请求内的行情、LLM、摘要和工具 |

## 6. 代码索引

| 目录 | 职责 |
|---|---|
| `internal/telegram` | long polling、命令路由、per-chat 队列、发送/编辑 |
| `internal/agent` | 分析编排、追问工具循环、摘要、Prompt、预算与校验 |
| `internal/analysis` | EMA/RSI/MACD/ATR/SuperTrend、支撑阻力、三重滤网 |
| `internal/market` | Symbol/Candle/Snapshot、Resolver/Provider、快照缓存 |
| `internal/llm` | Completer/Streamer 接口、JSON Schema、工具调用类型 |
| `internal/session` | 会话与消息模型、Store 接口 |
| `internal/storage/sqlite` | 仓储实现、迁移、用量记录 |
| `internal/render` | HTML 转义、消息拆分、观点卡片 |
| `internal/logging` | trace_id、日志脱敏 |
| `internal/config` | 环境变量解析与校验 |
| `internal/app` | 依赖装配和生命周期 |
