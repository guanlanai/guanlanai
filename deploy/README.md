# 部署说明

目标机器：Oracle Cloud 免费 VPS，通过本机 SSH 配置登录，Ubuntu 26.04 / `arm64`。

> 下文的 `YOUR_VPS` 替换为你本机的 SSH 别名或 host；不要把个人别名提交到仓库。

## 目录

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

## 首次准备

```sh
ssh YOUR_VPS
sudo mkdir -p /opt/guanlanai/prod /opt/guanlanai/staging
sudo chown -R ubuntu:ubuntu /opt/guanlanai
```

把 `deploy/prod/` 和 `deploy/staging/` 下的文件复制到对应目录，然后：

```sh
cd /opt/guanlanai/prod
cp .env.example .env
openssl rand -base64 32   # 填入 APP_MASTER_KEY
chmod 600 .env
mkdir -p data
```

`staging` 用另一个 Bot Token 和另一个 `APP_MASTER_KEY`，不要与生产共用。

`PUID` / `PGID` 必须与 `data/` 目录所有者一致；在当前 VPS 上 `ubuntu` 是 `1001:1001`，可用 `id -u` / `id -g` 确认。

## 镜像

本机构建并传到 VPS（Apple Silicon 默认构建 arm64）：

```sh
VERSION=phase6 make docker
docker save guanlanai:phase6 | gzip | ssh YOUR_VPS 'sudo docker load'
```

如果是 x86 本机，使用 `make docker-multi` 或 `docker buildx build --platform linux/arm64`。

或者直接在 VPS 上构建（1 OCPU 会比较慢）：

```sh
cd /opt/guanlanai/src
sudo docker build -t guanlanai:phase6 .
```

## 启动

```sh
cd /opt/guanlanai/staging
sudo env IMAGE=guanlanai:phase6 docker compose up -d
sudo docker compose ps
sudo docker inspect --format '{{.State.Health.Status}}' guanlanai-staging-bot-1
```

staging 冒烟通过后再启动 prod：

```sh
cd /opt/guanlanai/prod
sudo env IMAGE=guanlanai:phase6 docker compose up -d
```

## 每日备份

把 `deploy/backup.sh` 复制到 VPS 并安装 cron：

```sh
scp deploy/backup.sh YOUR_VPS:/opt/guanlanai/backup.sh
ssh YOUR_VPS 'chmod 700 /opt/guanlanai/backup.sh'
ssh YOUR_VPS '(crontab -l 2>/dev/null; echo "15 3 * * * /opt/guanlanai/backup.sh >> /opt/guanlanai/backup.log 2>&1") | crontab -'
```

脚本备份 prod/staging，保留 14 天，日志写入 `/opt/guanlanai/backup.log`。

## Grafana Cloud 指标（可选）

Grafana Cloud 的注册、获取 Remote Write URL、创建 Token 的更详细步骤见 [../docs/GRAFANA_CLOUD_SETUP.md](../docs/GRAFANA_CLOUD_SETUP.md)。

仓库提供 Grafana Cloud Free + Grafana Alloy 的采集模板：

- Grafana Cloud Free 当前额度：10k active series、14 天保留、3 个用户。
- `deploy/monitoring/alloy.alloy`：本地抓取 bot 的 `/metrics`，通过 remote_write 推送到 Grafana Cloud。
- `deploy/{prod,staging}/docker-compose.yml` 中的 `alloy` 服务默认放在 `monitoring` profile，不启用时不会启动。
- Alloy 只出站访问 Grafana Cloud，不需要开放入站端口，也不需要发布 `8080`。

### 1. 准备 Grafana Cloud

1. 注册 Grafana Cloud 免费账号并创建 Stack。
2. 在 Stack 的 Prometheus 详情页找到 **Remote Write Endpoint** 和 **Username**。
3. 创建一个 Access Policy Token，作用域至少包含 `metrics:write`；Token 只显示一次。

### 2. 配置环境变量

在 prod 和 staging 各自的 `.env` 中填写：

```env
GRAFANA_CLOUD_REMOTE_WRITE_URL=https://prometheus-xxx.grafana.net/api/prom/push
GRAFANA_CLOUD_METRICS_USERNAME=123456
GRAFANA_CLOUD_API_KEY=glc_xxx
```

`.env` 保持 `chmod 600`，不要提交到仓库。两套环境可以共用同一个 Token，也可以分开创建。

### 3. 同步 Alloy 配置

```sh
sudo mkdir -p /opt/guanlanai/monitoring
scp deploy/monitoring/alloy.alloy YOUR_VPS:/tmp/alloy.alloy
ssh YOUR_VPS 'sudo mv /tmp/alloy.alloy /opt/guanlanai/monitoring/alloy.alloy'
```

Compose 会以只读方式挂载 `../monitoring/alloy.alloy`。修改配置后可在本机运行：

```sh
make alloy-check
```

### 4. 启动采集

先只启动 staging 的 Alloy：

```sh
cd /opt/guanlanai/staging
sudo docker compose --profile monitoring up -d alloy
sudo docker logs --tail=100 -f guanlanai-staging-alloy-1
```

确认无误后再启动 prod：

```sh
cd /opt/guanlanai/prod
sudo docker compose --profile monitoring up -d alloy
```

如果启动或更新时同时拉起 bot：

```sh
sudo env IMAGE=<当前镜像tag> docker compose --profile monitoring up -d
```

### 5. 在 Grafana 验证

在 Grafana Cloud 的 Explore 中查询：

```promql
up{job="guanlanai-bot"}
guanlanai_requests_total{kind="analysis"}
guanlanai_llm_tokens_total
```

建议先确认 `up{deployment="guanlanai-staging"}` 和 `up{deployment="guanlanai-prod"}` 都为 `1`。

### 6. 停用采集

```sh
cd /opt/guanlanai/prod
sudo docker compose --profile monitoring stop alloy
```

停止 Alloy 不会影响 bot；如果确定不再使用，可以 `rm -sf` 删除容器，再按需删除 `guanlanai-prod_alloy-data` volume。

## 检查

```sh
sudo docker compose logs -f --tail=100
sudo docker inspect --format '{{.State.Health.Status}}' guanlanai-prod-bot-1
```

健康检查在容器内部执行，不向公网发布 8080。需要从宿主机检查时，可临时执行：

```sh
sudo docker compose exec bot wget -qO- http://127.0.0.1:8080/healthz
sudo docker compose exec bot wget -qO- http://127.0.0.1:8080/metrics
```

`/metrics` 是 Prometheus 指标端点，默认 `METRICS_ENABLED=true`；采集方式和关键指标见 [../docs/OPERATIONS.md](../docs/OPERATIONS.md)。

## 注意

- Oracle Security List/NSG 只开 SSH；不要放行 8080（健康检查或 `/metrics` 也不例外）。
- 两套环境的 Bot Token、`APP_MASTER_KEY`、SQLite、volume、备份目录必须独立。
- VPS 上 `api.binance.com` 返回 451，代码默认使用 `data-api.binance.vision`。
- 升级前先备份 `data/`，再切换镜像 tag。
