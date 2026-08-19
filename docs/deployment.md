# mini-ci-cd 部署指南

## 运行前提

- Linux 服务器。
- Go 构建出的 `minicicd` 二进制。
- Git CLI。
- 项目 Pipeline 所需的语言运行时和进程管理工具。
- 专用的非 root 系统用户。
- 提供 HTTPS 的反向代理。

mini-ci-cd 会以服务用户权限直接执行项目命令，只能用于受信任仓库。

## 目录和用户

```bash
sudo useradd --system --home /var/lib/mini-cicd --create-home --shell /usr/sbin/nologin minicicd
sudo install -d -o minicicd -g minicicd -m 0700 /var/lib/mini-cicd
sudo install -o root -g root -m 0755 minicicd /usr/local/bin/minicicd
```

不要使用 root 运行服务。需要写入应用发布目录时，只向 `minicicd` 用户授予对应目录的最小权限。

## 主密钥

生成 32 字节主密钥：

```bash
openssl rand -base64 32 | tr -d '='
```

将结果保存到权限为 `0600` 的服务环境文件。主密钥和数据库必须分别备份；丢失主密钥后，Git 凭据、Webhook Secret 和项目 Secret 无法恢复。

```bash
sudo install -o root -g root -m 0600 /dev/null /etc/mini-cicd.env
```

示例内容：

```text
MINICICD_LISTEN_ADDR=127.0.0.1:8080
MINICICD_DATA_DIR=/var/lib/mini-cicd
MINICICD_SECURE_COOKIES=true
MINICICD_MASTER_KEY=<base64-key>
MINICICD_GLOBAL_PARALLEL=2
MINICICD_SHELL=/bin/bash
```

## systemd

创建 `/etc/systemd/system/mini-cicd.service`：

```ini
[Unit]
Description=mini-ci-cd
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=minicicd
Group=minicicd
EnvironmentFile=/etc/mini-cicd.env
ExecStart=/usr/local/bin/minicicd
WorkingDirectory=/var/lib/mini-cicd
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/mini-cicd

[Install]
WantedBy=multi-user.target
```

如果 Deploy Step 需要写入其他目录，必须将具体目录加入 `ReadWritePaths`，不要放宽为整个文件系统。

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now mini-cicd
sudo systemctl status mini-cicd
```

## 反向代理

反向代理需要：

- 将外部 HTTPS 请求转发到 `127.0.0.1:8080`。
- SSE 日志接口关闭响应缓冲。
- 保留较长的读取超时。
- Webhook 请求体限制不超过 mini-ci-cd 的 1 MiB 上限。

Nginx 日志接口示例：

```nginx
location /api/v1/deployments/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_read_timeout 1h;
}
```

## Webhook

Webhook URL：

```text
https://deploy.example.com/api/v1/webhooks/<project-id>/<provider>
```

`provider` 为 `github`、`gitlab` 或 `gitea`。项目必须开启 Auto Deploy 并保存 Webhook Secret。

## Prometheus

Prometheus can scrape `GET /metrics`. The endpoint exposes deployment state,
pipeline cache hit/miss counters, and failed notification/provider deliveries.

- GitHub：验证 `X-Hub-Signature-256`。
- GitLab：常量时间比较 `X-Gitlab-Token`。
- Gitea：验证 `X-Gitea-Signature`。

系统还会校验 Push 事件、Delivery ID、仓库、目标分支和完整 Commit SHA。重复 Delivery ID 不会创建重复部署。

## 备份

需要一并备份：

- SQLite 数据库及 WAL 文件。
- `/var/lib/mini-cicd/logs`。
- `MINICICD_MASTER_KEY`，应与数据库分开保存。

执行一致性备份前建议暂停服务，或使用 SQLite 在线备份工具。
