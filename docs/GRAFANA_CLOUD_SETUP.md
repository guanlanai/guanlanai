# Grafana Cloud 接入步骤

本文按 Grafana Cloud 当前界面（2026 年）说明如何创建 Stack、获取 Prometheus Remote Write 地址和用户名、创建 Access Policy Token，并接入观澜的 Alloy 采集。

> 只需要三个值：`GRAFANA_CLOUD_REMOTE_WRITE_URL`、`GRAFANA_CLOUD_METRICS_USERNAME`、`GRAFANA_CLOUD_API_KEY`。不要使用 Grafana 登录密码，也不要把 Token 提交到仓库。

## 1. 创建或进入 Stack

1. 打开 <https://grafana.com/> 并登录。
2. 点击右上角 **My account**，进入 **Cloud Portal**。
3. 如果还没有 Stack，按页面提示创建一个免费 Stack；名称可以填 `guanlanai`。
4. 在 Cloud Portal 首页确认已经选中该 Stack。

Stack 就是一组 Grafana、Prometheus、Loki 等服务的集合。后面的 Remote Write 地址和 Token 都属于这个 Stack。

## 2. 获取 Remote Write URL 和 Username

1. 在 Cloud Portal 的 Stack 首页找到 **Prometheus**（有的界面显示为 **Metrics**）卡片。
2. 点击卡片上的 **Details**。
3. 找到 **Remote Write Endpoint**，复制完整地址，通常形如：

   ```text
   https://prometheus-prod-xx-prod-us-central-0.grafana.net/api/prom/push
   ```

   注意：
   - 必须包含末尾的 `/api/prom/push`。
   - 不要复制成查询地址 `/api/prom`。
   - 不要用其他区域的地址替换，直接使用页面显示的值。

4. 在同一页找到 **Username**，复制数字 ID，例如：

   ```text
   123456
   ```

   这个数字是 Metrics 实例 ID，不是 Grafana 登录邮箱或用户名。

如果找不到 Prometheus 卡片，可以在 Cloud Portal 里搜索 **Prometheus**，或进入 **Grafana** 实例后通过 **Connections** 查看 Prometheus 数据源详情。

## 3. 创建 Access Policy Token

Grafana Cloud 使用 Access Policy Token 做程序化写入。推荐只授予 `metrics:write`，不要使用管理员权限的 Token。

### 方式 A：从 Stack 内创建（推荐）

1. 进入该 Stack 的 Grafana 实例。
2. 左侧菜单点击 **Administration**。
3. 选择 **Cloud access policies**。如果菜单里有 **Users and access**，则进入 **Users and access** -> **Cloud access policies**。
4. 点击 **Create access policy**。
5. 填写：
   - **Display name**：`guanlanai-metrics-write`
   - **Name**：保持自动生成即可
6. 在 **Scopes** 中只勾选：

   ```text
   metrics:write
   ```

   如果列表里没有，使用 **Add scope** 搜索并添加。
7. 点击 **Create**。
8. 回到该 Access Policy，点击 **Add token**。
9. 填写：
   - **Display name**：`guanlanai-alloy`
   - **Expiration**：可留空，或按需设置
10. 点击 **Create**。
11. 页面只显示一次 Token，立即点击 **Copy to clipboard** 保存。Token 通常以 `glc_` 开头。

### 方式 B：从 Cloud Portal 创建

1. 在 Cloud Portal 左侧选择 **Security** -> **Access Policies**。
2. 点击 **Create access policy**。
3. 填写 Display name，例如 `guanlanai-metrics-write`。
4. **Realm** 选择当前 Stack（或包含该 Stack 的范围）。
5. Scopes 只选择 `metrics:write`。
6. 创建后进入该 Policy，点击 **Add token**。
7. 创建并复制 Token。

### 如果界面仍显示旧版 API Keys

部分旧账号/旧界面可能显示 **Security** -> **API Keys**：

1. 点击 **Add API key**。
2. 名称填 `guanlanai-alloy`。
3. Role 选择 **Metrics Publisher**。
4. 创建后复制 Key。

新账号优先使用 Access Policy + `metrics:write`，权限更小。

## 4. 填写环境变量

在 prod 和 staging 的 `.env` 中分别填写：

```env
GRAFANA_CLOUD_REMOTE_WRITE_URL=https://prometheus-prod-xx-prod-us-central-0.grafana.net/api/prom/push
GRAFANA_CLOUD_METRICS_USERNAME=123456
GRAFANA_CLOUD_API_KEY=glc_xxxxxxxxxxxxxxxxxxxxxxxx
```

检查：

```sh
grep '^GRAFANA_CLOUD_' .env
chmod 600 .env
```

确认：

- URL 完整且包含 `/api/prom/push`。
- Username 是数字 Metrics 实例 ID。
- Token 是 `glc_...`，不是 Grafana 登录密码。
- 值两侧没有引号、空格或换行；如果不小心粘贴了换行，重新复制。
- `.env` 已在 `.gitignore` 中，绝不提交。

两套环境可以共用同一个 Token，也可以分别创建；观澜会通过 `deployment` 和 `environment` 标签区分 prod/staging。

## 5. 同步 Alloy 配置并启动

在 VPS 上：

```sh
sudo mkdir -p /opt/guanlanai/monitoring
scp deploy/monitoring/alloy.alloy YOUR_VPS:/tmp/alloy.alloy
ssh YOUR_VPS 'sudo mv /tmp/alloy.alloy /opt/guanlanai/monitoring/alloy.alloy'
```

先启动 staging：

```sh
cd /opt/guanlanai/staging
sudo docker compose --profile monitoring up -d alloy
sudo docker logs --tail=100 -f guanlanai-staging-alloy-1
```

日志中应看到组件启动成功。若出现 `401`、`404`、`no such host` 或环境变量为空，先看下一节排查。

确认 staging 正常后再启动 prod：

```sh
cd /opt/guanlanai/prod
sudo docker compose --profile monitoring up -d alloy
```

## 6. 在 Grafana 中验证

1. 进入 Grafana Cloud Stack，打开 **Explore**。
2. 数据源选择 Grafana Cloud 自动创建的 **Prometheus** 数据源。
3. 输入查询：

   ```promql
   up{job="guanlanai-bot"}
   ```

4. 等待约 30–60 秒，确认：

   ```text
   up{deployment="guanlanai-staging", environment="staging", job="guanlanai-bot"} 1
   up{deployment="guanlanai-prod", environment="prod", job="guanlanai-bot"} 1
   ```

5. 再查询业务指标：

   ```promql
   guanlanai_requests_total{kind="analysis"}
   guanlanai_llm_tokens_total
   guanlanai_rate_limit_hits_total
   ```

如果 Explore 里没有 Prometheus 数据源，进入 **Connections** -> **Data sources** -> **Prometheus**，使用第 2 步的 URL、Username 和第 3 步的 Token 创建，Basic Auth 的密码填 Token。

## 7. 常见问题

### Alloy 容器反复重启

查看日志：

```sh
sudo docker logs --tail=200 guanlanai-prod-alloy-1
```

常见原因：

- `.env` 里三个 `GRAFANA_CLOUD_*` 为空或拼错。
- Token 已过期或被删除。
- URL 不是 `/api/prom/push`。
- 修改 `.env` 后没有重建 Alloy 容器：

  ```sh
  cd /opt/guanlanai/prod
  sudo docker compose --profile monitoring up -d --force-recreate alloy
  ```

### 日志出现 401 Unauthorized

- Token 复制不完整，或使用了 Grafana 登录密码。
- Access Policy 没有 `metrics:write` 权限。
- Token 已过期。
- Username 填成了邮箱，正确值是数字 Metrics 实例 ID。

处理：重新创建 Token，更新 `.env`，强制重建 Alloy。

### 日志出现 404 Not Found

- URL 少了 `/api/prom/push`。
- 复制了别的 Stack 或别的区域的地址。
- 使用了查询地址而不是 Remote Write 地址。

### Grafana 里没有数据

先确认 bot 指标正常：

```sh
cd /opt/guanlanai/prod
sudo docker compose exec -T bot wget -qO- http://127.0.0.1:8080/metrics | head
```

再确认 Alloy 是否在运行：

```sh
sudo docker compose --profile monitoring ps
```

最后检查 Grafana 查询的标签是否匹配：

```promql
{job="guanlanai-bot"}
```

### 担心免费额度

Grafana Cloud Free 当前为 10k active series、14 天保留。观澜两套环境通常只有几百个 series，远低于上限。真实活跃 series 可在 Grafana Cloud 的 **Usage** / **Billing** 页面查看。

### Token 泄露

立即在 Grafana Cloud 删除该 Token，创建新 Token，更新 `.env`，然后：

```sh
sudo docker compose --profile monitoring up -d --force-recreate alloy
```

## 8. 安全提醒

- Token 只授予 `metrics:write`，不要给 Admin 权限。
- `.env` 权限保持 `600`。
- 不要把 Token 写进 Alloy 配置、Dockerfile、Compose 文件或 Git。
- 不需要把 `8080` 发布到公网；Alloy 在 Docker 网络内访问 `bot:8080`，只做出站连接。
- 不再使用时：

  ```sh
  sudo docker compose --profile monitoring stop alloy
  ```
