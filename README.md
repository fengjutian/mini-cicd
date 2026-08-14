# mini-ci-cd

轻量、自托管、单机优先的 CI/CD 与应用部署平台。

当前实现处于 MVP 第一阶段，已经包含：

- Go HTTP 服务与 SQLite WAL 数据库。
- 首次启动创建唯一 Owner，初始化完成后关闭注册。
- Argon2id 密码哈希。
- HttpOnly、SameSite Session Cookie。
- 登录频率限制、同源校验和基础安全响应头。
- 初始化、登录、当前用户和退出 API。
- 无需前端构建工具即可使用的内嵌初始化/登录页面。
- 健康检查接口。
- Project、Pipeline、Git HTTPS/SSH 凭据和环境变量 API。
- Secret AES-256-GCM 加密及 Deployment 变量快照。
- Git Mirror 缓存、Branch 解析和固定 Commit SHA Checkout。
- SQLite 原子队列、项目锁和 Local Runner。
- Step/Deployment 超时、取消和跨平台进程树终止。
- 部署日志持久化、写前脱敏、大小限制和 SSE 续传。

完整产品需求见 [docs/requirements.md](docs/requirements.md)。

## 环境要求

- Go 1.26 或兼容版本。
- MVP 部署目标为 Linux；当前认证基础也可以在 Windows 开发环境运行。

SQLite 使用纯 Go 驱动，不要求 CGO。

## 本地运行

```powershell
go run ./apps/server/cmd/minicicd
```

浏览器访问 `http://127.0.0.1:8080`，首次打开时创建 Owner。

默认数据保存在当前目录的 `data/mini-cicd.db`。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MINICICD_LISTEN_ADDR` | `127.0.0.1:8080` | HTTP 监听地址 |
| `MINICICD_DATA_DIR` | `./data` | SQLite 与后续工作区的数据目录 |
| `MINICICD_SESSION_TTL` | `24h` | Session 有效期 |
| `MINICICD_SECURE_COOKIES` | `false` | HTTPS 环境必须设置为 `true` |
| `MINICICD_TRUST_PROXY` | `false` | 是否信任第一段 `X-Forwarded-For` |
| `MINICICD_MASTER_KEY` | 无 | 32 字节主密钥的无填充 Base64；使用 Secret 时必填 |
| `MINICICD_GLOBAL_PARALLEL` | `2` | 全局并行部署数 |
| `MINICICD_SHELL` | Linux `/bin/bash` | Local Pipeline 使用的固定 Shell |
| `MINICICD_LOG_MAX_BYTES` | `10485760` | 单次部署日志上限 |
| `MINICICD_CANCEL_GRACE` | `10s` | 强制终止前的等待时间 |
| `MINICICD_CLEANUP_INTERVAL` | `1h` | 后台清理周期 |
| `MINICICD_WORKSPACE_RETENTION` | `24h` | 终态部署工作区保留时间 |
| `MINICICD_LOG_RETENTION` | `720h` | 部署日志保留时间 |
| `MINICICD_DEPLOYMENT_RETENTION` | `100` | 每个项目保留的部署记录数 |

生产环境必须通过 HTTPS 提供服务，并将 `MINICICD_SECURE_COOKIES` 设置为 `true`。只有在服务位于可信反向代理后方时才能开启 `MINICICD_TRUST_PROXY`。

主密钥示例生成方式：

```powershell
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
[Convert]::ToBase64String($bytes).TrimEnd('=')
```

主密钥丢失后，Git 凭据与 Secret 无法恢复。数据库中已有密文但启动时未提供主密钥，服务将拒绝启动。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 服务与数据库健康状态 |
| `GET` | `/api/v1/status` | 是否已经初始化 |
| `POST` | `/api/v1/setup` | 创建唯一 Owner，仅初始化前可用 |
| `POST` | `/api/v1/auth/login` | 登录 |
| `GET` | `/api/v1/auth/me` | 获取当前用户 |
| `POST` | `/api/v1/auth/logout` | 注销当前 Session |
| `GET/POST` | `/api/v1/projects` | 项目列表与创建 |
| `GET/PUT/DELETE` | `/api/v1/projects/{id}` | 项目详情、更新与归档 |
| `GET` | `/api/v1/projects/{id}/variables` | 环境变量列表 |
| `PUT/DELETE` | `/api/v1/projects/{id}/variables/{name}` | 更新或删除变量 |
| `POST/GET` | `/api/v1/projects/{id}/deployments` | 创建部署或查看项目部署历史 |
| `GET` | `/api/v1/deployments/{id}` | 部署详情 |
| `GET` | `/api/v1/deployments/{id}/steps` | 部署步骤与执行结果 |
| `POST` | `/api/v1/deployments/{id}/cancel` | 幂等取消部署 |
| `POST` | `/api/v1/deployments/{id}/redeploy` | 重新部署历史 Commit |
| `GET` | `/api/v1/deployments/{id}/logs` | SSE 部署日志，支持 `Last-Event-ID` |
| `GET` | `/api/v1/dashboard` | Dashboard 统计 |
| `POST` | `/api/v1/webhooks/{projectId}/{provider}` | GitHub、GitLab 或 Gitea Push Webhook |

生产安装、反向代理和 Webhook 配置见 [docs/deployment.md](docs/deployment.md)。

## 验证

```powershell
go test ./...
go vet ./...
```
