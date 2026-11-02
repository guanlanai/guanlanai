# 运维手册

目标机：Oracle Cloud 免费 VPS，`ssh o1` 登录。Ubuntu 26.04 / `aarch64`，1 OCPU / 5.8GB / 45GB。

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
- 镜像：staging 和 prod 当前均为 `guanlanai:phase3`
- 容器用户：`PUID/PGID=1001`，与 `data/` 所有者一致
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

## 更新镜像

本机构建并传到 VPS：

```sh
VERSION=phase2 make docker
docker save guanlanai:phase2 | gzip | ssh o1 'sudo docker load'
```

先更新 staging：

```sh
ssh o1
cd /opt/guanlanai/staging
sudo env IMAGE=guanlanai:phase2 docker compose up -d
sudo docker inspect --format '{{.State.Health.Status}}' guanlanai-staging-bot-1
```

staging 验证通过后再更新 prod。正式发布建议用 git SHA 作为镜像 tag，便于回滚。

## 备份（Phase 0 临时方案）

Phase 0 还没有 `guanlanai backup` 子命令，先停机复制 SQLite：

```sh
cd /opt/guanlanai/prod
sudo mkdir -p /opt/guanlanai/backups
backup="/opt/guanlanai/backups/prod-$(date -u +%Y%m%dT%H%M%SZ)"
sudo mkdir -p "$backup"
sudo docker compose stop
sudo cp -a data/guanlanai.db* "$backup/"
sudo docker compose start
```

- 备份目录不要放在容器 volume 内。
- 生产/测试分开备份。
- 至少定期把备份拉到 VPS 外。
- Phase 5 会加 `VACUUM INTO` 备份子命令和恢复演练。

## 恢复

```sh
cd /opt/guanlanai/prod
sudo docker compose stop
sudo cp -a /opt/guanlanai/backups/<backup>/guanlanai.db* data/
sudo chown 1001:1001 data/*
sudo chmod 600 data/*
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
