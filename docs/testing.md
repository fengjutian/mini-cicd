# 测试与验收

## Linux 原生端到端测试

CI 在 `ubuntu-latest` 上先构建 React 生产资源，再执行：

```bash
npm ci --prefix apps/web
npm run build --prefix apps/web
go test -race ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./apps/server/...
go build ./apps/server/cmd/minicicd
```

服务端测试覆盖首次初始化、登录、Project/Secret、Git SHA 固化、数据库并发领取、Local Runner、取消、日志脱敏/SSE、Webhook 和服务重启恢复。

## 真实 SSH 私有仓库

仓库 Actions Secret 配置以下值后，CI 自动启用 `integration` 测试：

- `SSH_TEST_REPOSITORY`：例如 `git@github.com:org/private-repo.git`
- `SSH_TEST_PRIVATE_KEY`：只读 Deploy Key 私钥
- `SSH_TEST_KNOWN_HOSTS`：由可信渠道取得的主机公钥行

该测试不会关闭 Host Key 校验，会通过实际 `git ls-remote`、mirror fetch 和 Commit SHA 解析验证整条凭据注入链路。

本地可用同名 `MINICICD_TEST_*` 环境变量运行：

```bash
go test -tags=integration ./apps/server/internal/gitops -run TestRealSSHPrivateRepository -v
```

## 浏览器验收

生产构建后启动服务，依次验证首次初始化、登录、项目编辑三种认证方式、变量版本、部署详情、SSE 日志续传、取消/重部署、Webhook 历史、系统检查、密码与 Session 注销。窄屏宽度还需验证侧栏、表格和日志区域没有横向覆盖。
