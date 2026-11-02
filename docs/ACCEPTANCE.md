# 上线验收报告

- 日期：2026-11-02
- 环境：Oracle Cloud 免费 VPS（`aarch64`，region `iad`），prod + staging 同机
- 版本：`guanlanai:phase5`
- 结论：**有条件通过**。核心链路、Key 安全、会话、限流、备份和生产部署均通过；有两项既定目标未完全满足，见“偏差与待办”。

## 验收结果

| 项目 | 结果 | 证据 |
| --- | --- | --- |
| `/start → /key → BTC` 全流程 | ✅ 通过 | staging 与 prod 均实际完成配置、分析和返回卡片 |
| 首次观点 p95 < 10s | ⚠️ 未达标 | 实测 BTC 18.3s、SOL 23.2s、BNB 16.7s、Phase 4 BTC 22.6s；`gpt-5-mini` reasoning 占主要耗时 |
| Binance USDT 现货主流币 | ✅ 通过 | BTC/SOL/BNB 实际分析成功；单元测试覆盖代码解析和 K 线 |
| 非法/非支持代码提示 | ✅ 通过 | 返回“找不到该交易对，或暂不支持”，不会编造数据 |
| 股票代码提示 | ✅ 通过 | 错误提示明确包含“股票暂不支持”，并有单元测试 |
| Key 加密存储 | ✅ 通过 | DB 中 `key_status=valid`，密文 180 字节 BLOB，nonce 12 字节，无 `sk-` 明文 |
| Key 消息删除 | ✅ 通过 | 日志无删除失败记录；DB 不保存明文 |
| 日志无明文密钥 | ✅ 通过 | 容器日志检索 `sk-`、`Bearer`、Token/Master Key 模式均无命中 |
| `/status` 只显示掩码 | ✅ 通过 | 显示 `sk...ey` 形式，不显示完整 Key |
| 无 Key 不能分析 | ✅ 通过 | `handleAnalyze`/`handleFollowUp` 都要求 `HasKey()` |
| 服务端 Key 禁用 | ✅ 通过 | prod/staging `ALLOW_SERVER_KEY=false`，`OPENAI_API_KEY` 长度为 0 |
| 自定义 base_url | ✅ 通过（代码/测试） | 单元测试覆盖 http、私网、回环、云元数据、重定向、响应上限 |
| 仅私聊 | ✅ 通过 | 非 private chat 直接提示仅支持私聊 |
| 30 天消息清理 | ✅ 通过（代码） | 每小时 cleanup 任务按 `DATA_RETENTION_DAYS` 删除过期消息 |
| `/reset` 清空会话 | ✅ 通过 | staging 实测重置后追问提示“没有可追问的分析”，`generation` 递增 |
| 结构化输出校验 | ✅ 通过 | JSON 解析 + 方向/信心/止损/目标/理由/风险校验；失败修复一次，不展示坏 JSON |
| 观点数据时间/来源/风险提示 | ✅ 通过 | 卡片包含 UTC 时间、Binance 来源、数据质量、免责声明和风险 |
| 追问继承上下文 | ✅ 通过 | staging 实测“那止损呢？”、“如果跌破支撑怎么办？”成功 |
| 限流 | ✅ 通过（测试） | 分析 6/分钟、30/小时；追问 20/分钟；单元测试覆盖 |
| 全局 LLM 并发 | ✅ 通过 | `agent.Service` semaphore，大小来自 `LLM_MAX_CONCURRENCY` |
| 超时 | ✅ 通过 | 行情 10s、LLM 60s、分析总 75s；修复了 10s header timeout 导致的误判 |
| 重试 | ⚠️ 部分 | Binance 429/5xx 有重试；LLM 仅对 JSON 校验失败修复一次，无通用瞬时重试 |
| 熔断器 | ⏸ 未实施 | 已列为 P1 |
| VPS Docker/Compose | ✅ 通过 | Docker 29.1.3、Compose 2.40.3；arm64 镜像；两容器 healthy |
| 重启数据不丢 | ✅ 通过 | staging 重启后 `users=1 sessions=1` 不变，health healthy |
| 8080 不暴露公网 | ✅ 通过 | 宿主无 8080 监听；公网 curl 8080/8081 超时；容器仅内部 `8080/tcp` |
| Security List/NSG 只开 SSH | ⚠️ 需控制台确认 | 公网 111/8080/8081 均无响应；111 仍由 rpcbind 在宿主监听，控制台规则无法从代码侧确认 |
| VPS 访问 Binance/CoinGecko/OpenAI | ✅ 通过 | `data-api.binance.vision` 200、CoinGecko 200、OpenAI 可达；`api.binance.com` 451，代码默认 vision |
| prod/staging 隔离 | ✅ 通过 | Token/主密钥哈希不同；SQLite 路径、volume、compose project 独立 |
| 备份 | ✅ 通过 | prod 备份 49KB，`PRAGMA integrity_check=ok`，已复制到本机（VPS 外） |
| 恢复演练 | ✅ 通过（只读校验） | 本机打开备份，users/sessions/usage 可读；未对生产执行覆盖恢复 |
| 无真实下单 | ✅ 通过 | `TRADING_ENABLED=false`；配置开启会启动失败；代码无下单路径 |
| gofmt/vet/test-race | ✅ 通过 | `make fmt-check vet test`、`go test -race ./...` 全绿 |

## 证据

staging 分析日志：

```text
BTC  18.3s  prompt 778  completion 1959
SOL  23.2s  prompt 788  completion 1758
BNB  16.7s  prompt 767  completion 1832
Phase 4 BTC 22.6s prompt 786 completion 2573
```

备份：

```text
/data/backups/guanlanai-20261102T080409Z.db
-rw------- 49152 bytes
integrity_check = ok
users=1 sessions=1 usage=2
```

安全检查：

```text
prod/staging ALLOW_SERVER_KEY=false
prod/staging OPENAI_API_KEY 长度 0
容器日志无 sk- / Bearer / Token / Master Key 明文
公网 8080/8081/111 curl 超时
宿主 ss 无 8080/8081 监听
```

## 偏差与待办

1. **p95 < 10s 未达标**：当前 `gpt-5-mini` 实际 17–23 秒。可选方案：
   - 将验收目标调整为 `< 30s`；
   - 对 gpt-5 设置 `reasoning_effort=low`；
   - 切换 `gpt-4.1-mini` 等非推理模型。
2. **熔断器未实施**：当前是重试 + 主备源 + 超时；熔断器列为 P1。
3. **CoinGecko 兜底未实施**：当前仅 Binance USDT 现货；非 Binance 币种明确提示不支持，CoinGecko 列为 P1。
4. **rpcbind 111**：外网 curl 超时，但宿主仍在监听；无 NFS 需求时建议禁用，并在 Oracle 控制台确认 Security List 只放行 SSH。
5. **备份计划**：已做一次 VPS 外备份；建议配置每日 cron 和恢复演练记录。

## 上线结论

核心产品链路可用，生产已稳定运行。建议按“有条件通过”上线，并在下一轮 P1 处理熔断器、CoinGecko 和 p95 目标。
