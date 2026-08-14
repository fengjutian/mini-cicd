# Local Runner 权限隔离

生产模式由三个不同的安全主体组成：

1. `minicicd` 控制面用户：读取数据库、主密钥、Git 凭据和日志，但不执行项目命令。
2. `minicicd-runner` daemon：监听 Unix Socket，只负责验证请求、降权和管理进程树；daemon 不加载数据库或主密钥。
3. `minicicd-job` 用户：实际执行 Build/Deploy Step，只能访问独立工作目录，不能访问控制面数据目录或 Runner Socket。

控制面完成凭据受控的 Git Checkout 后，通过 `/run/minicicd/runner.sock` 流式发送命令、环境快照和超时。Socket 仅属于 `minicicd-control` 组，job 用户只属于 `minicicd-workspace` 组。Runner 强制串行执行，结束后清理整个进程组，避免并发任务之间通过进程环境窃取 Secret。

生产环境必须设置：

```text
MINICICD_RUNNER_ENDPOINT=/run/minicicd/runner.sock
MINICICD_RUNNER_WORKSPACE_DIR=/var/lib/minicicd-workspaces
```

Runner daemon 使用独立配置：

```text
MINICICD_RUNNER_SOCKET=/run/minicicd/runner.sock
MINICICD_RUNNER_WORKSPACE_DIR=/var/lib/minicicd-workspaces
MINICICD_RUNNER_SOCKET_GID=<minicicd-control gid>
MINICICD_RUNNER_JOB_UID=<minicicd-job uid>
MINICICD_RUNNER_JOB_GID=<minicicd-workspace gid>
```

未配置 Endpoint 时仍保留进程内 Runner，方便 Windows 开发和测试；系统检查会将其标记为安全警告，不应在生产环境使用。
