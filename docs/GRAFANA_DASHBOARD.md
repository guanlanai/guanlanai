# Grafana 看板

看板 JSON：`deploy/monitoring/dashboards/guanlanai-overview.json`

- UID：`guanlanai-overview`
- 标题：观澜 GuanLanAI 概览
- 默认时间范围：最近 6 小时
- 刷新频率：30 秒
- 数据源：Dashboard 变量 `datasource`，导入后选择 Grafana Cloud 的 Prometheus
- 部署筛选：变量 `deployment`，可多选 `guanlanai-prod` / `guanlanai-staging`
- 当前不包含告警规则

## 面板内容

| 分组 | 面板 |
|---|---|
| 总览 | Bot 存活、请求速率、请求错误率、请求 P95 延迟 |
| 请求与限流 | 按 kind 的请求速率/错误率/P95 延迟、在途请求、限流命中 |
| LLM 与 Token | LLM 调用速率、LLM 错误率、LLM P95 延迟、Token 用量 |
| Telegram 与行情 | Telegram Update 处理速率、Telegram 发送错误速率、行情错误率、行情 P95 延迟 |
| 运行时与版本 | Go Goroutines、进程 RSS、`guanlanai_build_info` 构建信息 |

## 导入方式 A：Grafana UI

1. 登录 Grafana Cloud，进入对应 Stack 的 Grafana。
2. 左侧菜单选择 **Dashboards**。
3. 点击 **New** -> **Import**。
4. 上传 `deploy/monitoring/dashboards/guanlanai-overview.json`，或直接粘贴 JSON。
5. 如果页面要求选择数据源，选择 Grafana Cloud 的 **Prometheus**。
6. 点击 **Import**。
7. 在 Dashboard 顶部确认：
   - `数据源` 选择 Prometheus
   - `部署` 选择 All 或具体环境

## 导入方式 B：HTTP API

适用于重复导入或通过脚本更新。

### 1. 创建 Grafana Service Account Token

1. 在 Grafana 实例左侧进入 **Administration** -> **Users and access** -> **Service accounts**。
2. 点击 **Add service account**。
3. 名称填 `guanlanai-dashboard-import`，角色选择 **Editor** 或 **Admin**。
4. 创建后点击 **Add service account token**。
5. 创建 Token 并立即复制。这个 Token 不是 `metrics:write` 的 Cloud Access Policy Token。

### 2. 使用脚本导入

```sh
export GRAFANA_URL=https://your-stack.grafana.net
export GRAFANA_TOKEN=glsa_xxxxxxxxxxxxxxxxx
./deploy/monitoring/import-dashboard.sh
```

脚本需要本机安装 `curl` 和 `jq`。

也可以手动调用 API：

```sh
jq '{dashboard: ., overwrite: true, message: "import guanlanai overview"}' \
  deploy/monitoring/dashboards/guanlanai-overview.json > /tmp/guanlanai-dashboard.json

curl -fsS \
  -H "Authorization: Bearer $GRAFANA_TOKEN" \
  -H 'Content-Type: application/json' \
  -X POST "$GRAFANA_URL/api/dashboards/db" \
  --data-binary @/tmp/guanlanai-dashboard.json
```

成功时返回类似：

```json
{
  "status": "success",
  "uid": "guanlanai-overview",
  "url": "/d/guanlanai-overview/..."
}
```

再次导入会覆盖同 UID 的看板。

## 常见问题

### 面板显示 No data

- 确认 Grafana 顶部 `数据源` 已选择 Prometheus。
- 确认 `部署` 变量不是空值。
- 先查询 `up{job="guanlanai-bot"}`，确认存在时间序列。
- 业务计数在还没有分析/追问请求时可能为 0；Go runtime、process、`up` 等指标会持续有数据。
- 检查时间范围是否覆盖数据写入时间。

### 导入后中文标题乱码

使用 Grafana UI 上传 JSON 文件，或确保 API 请求头包含：

```text
Content-Type: application/json
```

并直接使用 UTF-8 编码的原始 JSON 文件。

### 想调整面板

建议在 Grafana UI 中编辑后导出 JSON，再覆盖仓库文件并提交，保持看板可复现。
