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

## 检查

```sh
sudo docker compose logs -f --tail=100
sudo docker inspect --format '{{.State.Health.Status}}' guanlanai-prod-bot-1
```

健康检查在容器内部执行，不向公网发布 8080。需要从宿主机检查时，可临时执行：

```sh
sudo docker compose exec bot wget -qO- http://127.0.0.1:8080/healthz
```

## 注意

- Oracle Security List/NSG 只开 SSH；不要放行 8080。
- 两套环境的 Bot Token、`APP_MASTER_KEY`、SQLite、volume、备份目录必须独立。
- VPS 上 `api.binance.com` 返回 451，代码默认使用 `data-api.binance.vision`。
- 升级前先备份 `data/`，再切换镜像 tag。
