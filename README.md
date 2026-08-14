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

生产环境必须通过 HTTPS 提供服务，并将 `MINICICD_SECURE_COOKIES` 设置为 `true`。只有在服务位于可信反向代理后方时才能开启 `MINICICD_TRUST_PROXY`。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 服务与数据库健康状态 |
| `GET` | `/api/v1/status` | 是否已经初始化 |
| `POST` | `/api/v1/setup` | 创建唯一 Owner，仅初始化前可用 |
| `POST` | `/api/v1/auth/login` | 登录 |
| `GET` | `/api/v1/auth/me` | 获取当前用户 |
| `POST` | `/api/v1/auth/logout` | 注销当前 Session |

## 验证

```powershell
go test ./...
go vet ./...
```
