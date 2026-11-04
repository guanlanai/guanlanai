# 运维手册

目标机：Oracle Cloud 免费 VPS，通过本机 SSH 配置登录。Ubuntu 26.04 / `aarch64`，1 OCPU / 5.8GB / 45GB。

> 下文的 `YOUR_VPS` 替换为你本机的 SSH 别名或 host；不要把个人别名提交到仓库。

## 目录与服务

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

- 生产容器：`guanlanai-prod-bot-1`
- 测试容器：`guanlanai-staging-bot-1`
- 采集容器：`guanlanai-prod-alloy-1`、`guanlanai-staging-alloy-1`
- 镜像：bot 为 `guanlanai:metrics-v1`；Alloy 为 `grafana/alloy:v1.21.3`
- 容器用户：`PUID/PGID=1001`，与 `data/` 所有者一致；Alloy 使用镜像内 UID 473
- 不发布宿主端口；健康检查通过容器内 `wget` 访问 `/healthz`

## 日常检查

```sh
cd /opt/guanlanai/prod
sudo docker compose ps
sudo docker inspect --format '{{.State.Health.Status}}' guanlanai-prod-bot-1
sudo docker compose logs --tail=100 -f
docker stats --no-stream
```

容器内健康检查：

```sh
sudo docker compose exec -T bot wget -qO- http://127.0.0.1:8080/healthz
sudo docker compose exec -T bot wget -qO- http://127.0.0.1:8080/readyz
```

## Prometheus 指标

`/metrics` 与健康检查共用容器内 `8080`，默认由 `METRICS_ENABLED=true` 开启。它**不发布到宿主端口**；需要查看时在容器内请求：

```sh
sudo docker compose exec -T bot wget -qO- http://127.0.0.1:8080/metrics
```

常用指标：

| 指标 | 用途 |
|---|---|
| `guanlanai_requests_total{kind,status}` | 分析/追问/摘要请求数与错误率 |
| `guanlanai_request_duration_seconds{kind}` | 请求延迟直方图 |
| `guanlanai_requests_in_flight{kind}` | 正在处理的请求数 |
| `guanlanai_llm_calls_total{operation,status}` | LLM 调用次数与错误率 |
| `guanlanai_llm_call_duration_seconds{operation}` | LLM 调用延迟 |
| `guanlanai_llm_tokens_total{operation,type}` | prompt/completion token 用量 |
| `guanlanai_rate_limit_hits_total{kind}` | 按用户限流命中 |
| `guanlanai_telegram_updates_total{result}` | Telegram update 处理结果 |
| `guanlanai_telegram_messages_total{status}` | Telegram 发送成功/失败 |
| `guanlanai_market_calls_total{operation,status}` | 行情解析/快照调用与错误率 |
| `guanlanai_market_call_duration_seconds{operation}` | 行情调用延迟 |
| `guanlanai_build_info{instance,env,version,commit}` | 实例和镜像版本 |

说明：

- 指标标签不包含用户 ID、chat ID、币种或模型名，避免高基数；不要自行添加这类标签。
- 关闭指标：在 `.env` 设置 `METRICS_ENABLED=false` 并重启容器；健康检查不受影响。
- 外部 Prometheus 采集时不要把 `8080` 发布到公网。可先绑定 `127.0.0.1:8080`，再通过 SSH 隧道或私有网络采集；Oracle Security List/NSG 仍只开 SSH。
- 告警建议优先看：分析错误率、`guanlanai_llm_calls_total{status="error"}` 增长、LLM 延迟、限流命中、Telegram 发送失败和行情错误。

### Grafana Cloud + Alloy（可选）

仓库已包含免费采集模板：`deploy/monitoring/alloy.alloy`，以及 prod/staging compose 中的 `alloy` 服务（`monitoring` profile）。Alloy 在 VPS 本地抓取 `/metrics`，通过 HTTPS 出站 remote_write 到 Grafana Cloud Free，不开放入站端口。首次开通见 [GRAFANA_CLOUD_SETUP.md](GRAFANA_CLOUD_SETUP.md)。

```sh
cd /opt/guanlanai/prod
sudo docker compose --profile monitoring up -d alloy
sudo docker logs --tail=100 -f guanlanai-prod-alloy-1
```

验证抓取：

```promql
up{job="guanlanai-bot"}
up{deployment="guanlanai-prod"}
guanlanai_requests_total{kind="analysis"}
guanlanai_llm_tokens_total
```

- 免费额度目前为 10k active series、14 天保留、3 个用户；当前两套环境远低于该额度。
- Alloy 只接收 `APP_ENV`、`INSTANCE_NAME` 和 `GRAFANA_CLOUD_*`，不接收 Bot Token 或用户 Key。
- 不在使用时：`sudo docker compose --profile monitoring stop alloy`，bot 不受影响。
- 详细开通步骤见 [GRAFANA_CLOUD_SETUP.md](GRAFANA_CLOUD_SETUP.md)。

## 更新镜像

本机构建并传到 VPS：

```sh
VERSION=metrics-v1 make docker
docker save guanlanai:metrics-v1 | gzip | ssh YOUR_VPS 'sudo docker load'
```

先更新 staging：

```sh
ssh YOUR_VPS
cd /opt/guanlanai/staging
sudo env IMAGE=guanlanai:metrics-v1 docker compose up -d bot
sudo docker inspect --format '{{.State.Health.Status}}' guanlanai-staging-bot-1
```

staging 验证通过后再更新 prod。正式发布建议用 git SHA 作为镜像 tag，便于回滚。

## 备份

使用内置命令生成一致性备份（SQLite `VACUUM INTO`，不需要停机）：

```sh
cd /opt/guanlanai/prod
sudo docker compose exec -T bot guanlanai backup
```

默认写入容器 `/data/backups`，对应宿主机：

```text
/opt/guanlanai/prod/data/backups/
```

也可以指定路径：

```sh
sudo docker compose exec -T bot guanlanai backup -db /data/guanlanai.db -out /data/backups
```

每日备份脚本已安装：

```text
/opt/guanlanai/backup.sh
cron: 15 3 * * * /opt/guanlanai/backup.sh >> /opt/guanlanai/backup.log 2>&1
```

脚本会依次备份 prod/staging，保留 14 天，并用 `flock` 防止重叠执行。脚本源码在 `deploy/backup.sh`。

- 备份文件权限为 `0600`。
- 生产/测试分开备份；至少定期把备份拉到 VPS 外。
- 建议定期做恢复演练。

## 恢复

```sh
cd /opt/guanlanai/prod
sudo docker compose stop
sudo cp -a /opt/guanlanai/prod/data/backups/<backup>.db data/guanlanai.db
sudo rm -f data/guanlanai.db-wal data/guanlanai.db-shm
sudo chown 1001:1001 data/guanlanai.db
sudo chmod 600 data/guanlanai.db
sudo docker compose start
```

## 回滚

```sh
cd /opt/guanlanai/prod
sudo env IMAGE=guanlanai:<old-tag> docker compose up -d
```

数据库迁移只向前；回滚镜像前确认旧版本能读当前 schema。

## 安全

- Oracle Security List/NSG 只开 SSH；不要放行 8080。
- 111（rpcbind）目前在监听；无 NFS 需求时建议：
  ```sh
  sudo systemctl disable --now rpcbind rpcbind.socket
  ```
  并确认 Security List 未放行 111。
- `.env` 权限保持 `600`；生产/测试的 `APP_MASTER_KEY` 不同。
- 不要 `docker compose down -v` 误删数据 volume（当前使用 bind mount，仍要小心）。
