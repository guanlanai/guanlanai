# 分析周期与个人偏好设计

> 状态：设计完成，待实施。
> 目标：把“分析周期”和“用户偏好”从 Binance/多周期临时逻辑中抽出来，形成跨行情源的交易助手配置体系。

## 1. 目标与非目标

### 目标

- 周期命名和用户输入统一采用 TradingView 惯例，不暴露 Binance interval 命名。
- Phase 1 每次分析有且只有一个明确的主周期；框架按 screen 集合设计，单周期只是一个 screen。
- 用户没有偏好时使用系统默认周期 `1D`，并在 `/status` 和分析提示中可见。
- 用户可以查看、设置和重置自己的常用周期。
- 一次分析只拉取一次所需周期的 K 线；后续切换指标、追问或重新渲染时，优先复用已拉取的 K 线。
- 为后续“常用指标”“风险/仓位偏好”和“多周期联合分析（如三重滤网）”留出稳定接口。

### 非目标（本阶段）

- 不做多周期共振分析；后续如需要，作为可选高级能力另开设计。
- 不做自动学习或从聊天记录推断偏好；偏好必须由用户显式设置。
- 不接入新行情源；只把行情源适配层与周期模型解耦。
- 不做真实下单。

## 2. 已确定的产品决策

1. **周期采用 TradingView 惯例命名**，内部模型不出现 Binance 的 `1d/1w` 命名。
2. **Phase 1 一次分析一个主周期**，默认 `1D`；分析计划按 screen 集合建模，单周期是只有一个 screen 的特例。
3. **K 线只拉一次**：同一会话内切换指标时，不重新请求行情源。
4. 用户偏好按用户全局保存；私聊和群组共用同一套偏好。
5. 群组不允许修改周期/指标/风险偏好，统一提示回私聊配置。
6. 三重滤网等多周期策略属于后续阶段，复用同一套周期、缓存和 screen 模型，不重写基础架构。

## 3. 周期模型

### 3.1 标准周期

```text
1m, 5m, 15m, 30m, 1h, 2h, 4h, 6h, 12h, 1D, 1W, 1M
```

- `1D`、`1W`、`1M` 使用 TradingView 的大写惯例。
- 内部类型使用 `market.Timeframe`，不是裸字符串。
- 用户输入允许别名：`D` → `1D`，`W` → `1W`，`M` → `1M`，`1d` → `1D`，`1w` → `1W`。
- 不支持的周期直接拒绝，并列出可用周期。

### 3.2 行情源映射

行情源适配器负责把标准周期映射成自己的参数，例如 Binance：

| 标准周期 | Binance interval |
| --- | --- |
| `1m` | `1m` |
| `1h` | `1h` |
| `4h` | `4h` |
| `1D` | `1d` |
| `1W` | `1w` |
| `1M` | `1M` |

映射只存在于 `internal/market/binance`，`agent`、`analysis`、`telegram` 和数据库都只使用标准周期。

### 3.3 默认周期

- 配置项：`DEFAULT_TIMEFRAME=1D`
- 用户未设置偏好时使用该值。
- `/status` 显示“当前周期：1D（默认）”。
- 分析开始提示中显示周期，例如“正在分析 BTC（1D · 波段）…”。

## 4. 数据模型

### 4.1 迁移 0003

```sql
ALTER TABLE users ADD COLUMN preferred_timeframe TEXT;
ALTER TABLE sessions ADD COLUMN current_timeframe TEXT;
```

- `users.preferred_timeframe`：用户显式偏好；空字符串表示未设置。
- `sessions.current_timeframe`：最近一次分析使用的周期，用于 `/status` 和会话展示。
- 索引不需要新增。
- 本迁移只做向前 ALTER，不重建表；上线前仍按惯例备份。

### 4.2 后续迁移预留

指标偏好和风险偏好进入后续阶段时再新增迁移，避免一次引入过多空字段。

## 5. 行情快照与复用

### 5.1 周期快照集合

`market.Snapshot` 按“一次分析所需的周期集合”建模；Phase 1 集合里只有一个周期，三重滤网时是三个。

```go
type Series struct {
    Timeframe Timeframe
    Candles   []Candle
    Partial   bool
    AsOf      time.Time
}

type Snapshot struct {
    Symbol    Symbol
    Series    map[Timeframe]Series
    Price     float64
    ChangePct float64
    FetchedAt time.Time
    Source    string
}

type Provider interface {
    Snapshot(ctx context.Context, symbol Symbol, timeframes []Timeframe) (*Snapshot, error)
}
```

- Phase 1 调用 `Snapshot(ctx, symbol, []Timeframe{primary})`。
- 三重滤网调用 `Snapshot(ctx, symbol, []Timeframe{tide, wave, entry})`。
- 不再由 `mode` 决定拉哪些周期；`mode` 只保留为分析风格（短线/波段），影响提示词和输出表达。
- 行情源适配器只负责把每个标准周期映射到自己的参数。

### 5.2 一次拉取，多次计算

```text
用户发起分析
  -> 解析 symbol
  -> 读取分析计划：Phase 1 是 [primary]，三重滤网是 [tide, wave, entry]
  -> 查会话快照缓存
      命中：直接复用对应周期的 candles
      未命中：向行情源请求缺失周期
  -> 对每个周期确定性计算指标
  -> 跨周期规则/对齐（单周期时跳过）
  -> LLM 生成观点
  -> 快照集合写入会话运行时缓存
```

后续“切换指标”只做：

```text
读取会话快照集合 -> 重新计算选中指标 -> 重新渲染/提问
```

不重新请求行情源。

### 5.3 缓存策略

- 进程内缓存，键为 `(symbol, timeframe)`，按周期独立复用。
- 会话保存最近一次 `*market.Snapshot`（可包含多个周期）；同一会话优先使用该快照。
- 缓存上限建议 1000 个周期序列，按最近使用时间淘汰，避免群组长期运行内存膨胀。
- TTL 按周期设置：
  - `1m`–`30m`：2 分钟
  - `1h`–`4h`：10 分钟
  - `1D`–`1W`：30 分钟
  - `1M`：2 小时
- 超过 TTL 或进程重启后，下一次分析/指标切换允许重新拉取。
- 缓存命中不改变 `FetchedAt`，展示时仍使用原始数据时间。

## 6. 用户命令与 UX

### 6.1 `/timeframe`

```text
/timeframe              查看当前周期和可用周期
/timeframe 1h           设置常用周期
/timeframe reset        恢复系统默认 1D
```

- 仅私聊可用；群组提示“请在私聊中配置”。
- 输入非法时返回可用列表，不静默回退。
- 设置成功后确认，例如“常用周期已设置为 1h”。

### 6.2 分析流程

- 普通发币种、`/analyze` 和追问都使用当前会话的周期。
- 分析开始时捕获 `sessionID + timeframe`；在途结果写回捕获的会话和周期。
- 用户修改偏好只影响下一次分析；不改变已有会话的历史分析。
- `/status` 显示：当前会话、当前周期、当前标的、今日用量。

### 6.3 周期未设置

- 不阻塞分析，直接使用系统默认 `1D`。
- 首次分析提示中带出周期；`/status` 标明“默认”。
- 引导文案示例：“当前使用默认周期 1D；发送 /timeframe 1h 可修改。”

## 7. 报告结构

`analysis.Report` 按 screen 集合建模；Phase 1 只有一个 screen。

```json
{
  "symbol": "BTC",
  "pair": "BTCUSDT",
  "market": "crypto",
  "mode": "swing",
  "strategy": "single",
  "as_of": "2026-11-03T00:00:00Z",
  "price": 68432.1,
  "change_pct": 1.23,
  "screens": [
    {
      "role": "primary",
      "timeframe": "1D",
      "as_of": "2026-11-03T00:00:00Z",
      "data_quality": "ok",
      "indicators": {
        "trend": "up",
        "ema20": 68010.5,
        "ema50": 67420.0,
        "rsi14": 58.2,
        "macd_hist": 42.0,
        "atr_pct": 1.1,
        "supertrend": {
          "value": 66100.0,
          "direction": "up",
          "distance_pct": 3.5,
          "bars_since_flip": 12,
          "period": 10,
          "multiplier": 3
        },
        "volume_ratio": 1.35,
        "support": [66800.0, 65200.0],
        "resistance": [70500.0, 72000.0]
      }
    }
  ],
  "data_quality": "ok",
  "source": "Binance"
}
```

三重滤网阶段只增加 screen，不改变外层结构：

```json
{
  "strategy": "triple_screen",
  "screens": [
    {"role": "tide",  "timeframe": "1W", "indicators": {}},
    {"role": "wave",  "timeframe": "1D", "indicators": {}},
    {"role": "entry", "timeframe": "1h", "indicators": {}}
  ],
  "alignment": {
    "tide": "up",
    "wave": "pullback",
    "entry": "triggered",
    "conflict": false
  }
}
```

- `strategy=single` 时，`screens` 长度为 1，`role=primary`。
- 旧会话里的历史 `last_analysis_json` 不解析结构，只作为追问上下文，因此不需要数据迁移。
- 新分析写入新结构。

## 8. 多周期与三重滤网演进

三重滤网（Elder Triple Screen）可以直接复用本设计的周期、快照、缓存和 screen 模型：

```text
第一重：潮汐（长周期，定方向）   例如 1W / 1D
第二重：波浪（中周期，找回调）   例如 1D / 4h
第三重：涟漪（短周期，找入场）   例如 1h / 15m
```

实现方式：

1. 一个策略配置声明 screen 列表：`role + timeframe + indicators`。
2. `Provider.Snapshot` 一次接收多个周期，内部并行拉取。
3. 每个周期独立计算指标，各自保留 `as_of` 和数据质量。
4. 增加确定性跨周期规则层，例如：
   - 长周期方向为多，只允许中周期回调做多；
   - 中周期振荡指标处于超卖/回调区；
   - 短周期出现突破/翻转触发。
5. 将跨周期结论压缩成 `alignment` 字段给 LLM，LLM 只负责解释和风险表达，不负责计算对齐。
6. 输出仍走统一结构化观点，必要时增加 `screen_decisions` 或 `conflict` 字段。

需要特别注意：

- **未收盘 K 线**：1D/1W/1M 当前蜡烛可能还在形成，使用它计算趋势会漂移。设计上应区分 `forming` 和 `closed`，高周期默认使用最近已收盘 K 线，或同时展示两者。
- **时间对齐**：不同周期的 `as_of` 不同，报告必须逐 screen 保留时间戳，不能只保留一个全局时间。
- **部分数据**：新币的 1W/1M 可能不足 50 根，三重滤网应降级为可用的 screen 组合，并明确标记，而不是直接失败。
- **token 预算**：三个 screen 的指标摘要会明显变长，需要压缩字段、限制指标数量，并优先保留跨周期结论。
- **延迟**：多周期并行拉取，但整体仍受行情源限流约束；超时后允许返回部分 screen 并标记 partial。

结论：**只要 Phase 1 就把 Provider、Snapshot、Report 和缓存按“周期集合”建模，三重滤网只是增加 screen 和跨周期规则，不需要推翻基础架构。**

## 9. 指标偏好（Phase 2）

暂不在 Phase 1 实现，但接口按以下方向预留：

```text
/indicators
/indicators ema,rsi,macd,atr,supertrend,volume,support_resistance
/indicators reset
```

- 用户选择只影响“传给 LLM 的指标”，基础风控指标（ATR、支撑阻力）始终保留。
- 指标切换从会话快照重新计算，不发新行情请求。
- 指标上限建议 8 个，避免提示词过载和过拟合。

## 10. 风险与仓位偏好（Phase 3）

- 用户设置“每笔最大亏损”，不要求提供账户总额。
- 仓位由确定性代码计算：`每笔风险金额 / |入场价 - 止损价|`。
- 未设置止损或风险偏好时，不给仓位建议。
- 默认禁止杠杆；仓位建议必须附风险提示。
- 群组不展示、不修改用户风险金额。

## 11. 配置

```dotenv
DEFAULT_TIMEFRAME=1D
```

- 启动时校验必须是标准周期。
- `DEFAULT_TIMEFRAME` 只作为未设置偏好时的默认值，不覆盖用户配置。

## 12. 测试计划

### 周期模型

- `1m`、`1D`、`1W`、`1M` 及别名 `D/W/M/1d/1w` 归一化。
- 非法周期被拒绝。
- 默认周期为 `1D`。

### 行情适配

- Binance 映射 `1D -> 1d`、`1W -> 1w`、`1M -> 1M`。
- 单周期请求只产生一次 K 线调用。

### 仓储

- 用户周期偏好读写、清空。
- 会话 `current_timeframe` 保存和读取。
- 迁移 0003 在已有用户/会话数据上执行成功。

### Telegram

- `/timeframe` 查看、设置、重置。
- 非法输入提示。
- 群组拒绝修改。
- 分析使用用户偏好或默认周期。

### 分析

- 报告只包含一个周期。
- SuperTrend 等指标在标准周期上正常计算。
- 1M 数据不足时标记 partial。

### 验收

- 未设置偏好时，分析使用 1D，并在状态中可见。
- 设置 `/timeframe 4h` 后，下一次分析使用 4h。
- 同一会话切换指标不触发新的 K 线请求。
- `go test -race ./...` 全绿。

## 13. 分阶段实施

### Phase 1：周期模型与单 screen 分析

- 新增 `market.Timeframe`、归一化、默认值。
- 迁移 0003、用户偏好和会话周期字段。
- Provider 改为周期集合 Snapshot（Phase 1 只传一个周期），Binance 做映射。
- `analysis.Report` 改为 `strategy + screens`，Phase 1 只有一个 `primary` screen。
- `/timeframe`、`/status`、分析提示。

### Phase 2：指标偏好与快照复用

- 会话快照缓存、LRU/TTL。
- `/indicators`、按选择计算指标。
- 指标切换不重新拉 K 线。

### Phase 3：风险画像与仓位计算

- `/risk`、确定性仓位计算、输出卡片扩展。
- 风控校验和测试。

### Phase 4：三重滤网/多屏策略

- 策略配置声明 `tide/wave/entry` 三个 screen。
- 多周期并行拉取和逐 screen 指标计算。
- 确定性跨周期对齐规则和 `alignment` 输出。
- 未收盘 K 线、partial 数据和 token 预算处理。

## 14. 开放问题

1. `/timeframe` 是否只允许私聊？本文按“是”设计，与 Key/model/mode 一致。
2. Phase 1 是否只跑一个 `primary` screen？本文按“是”设计，单周期是 screen 集合的特例。
3. 指标偏好的默认集合是否等同当前全部指标？建议默认全部，用户可裁剪。
4. 风险偏好是否存绝对金额（USDT）还是百分比？建议两者都支持，但只存用户显式输入的值。
5. 三重滤网的默认周期组合是 `1W/1D/1h` 还是可配置模板？建议先提供模板，再允许用户微调。
