# mini-ci-cd 产品需求文档

> 文档状态：草案 1.0  
> 产品阶段：MVP 规划  
> 更新日期：2026-08-14

## 1. 产品概述

### 1.1 产品定位

mini-ci-cd 是一个轻量、自托管、单机优先的 CI/CD 与应用部署平台，面向个人开发者、小型团队和 VPS 用户。

平台围绕以下核心流程提供统一操作界面：

```text
Git Push → 获取代码 → 执行流水线 → 部署应用 → 健康检查 → 查看结果
```

mini-ci-cd 不以替代 GitHub Actions、GitLab CI/CD 或 Jenkins 为目标，而是解决小型项目在单台服务器上依赖 SSH 和手工命令部署的问题。

### 1.2 核心价值

- 用户完成初次配置后，日常部署只需要执行 `git push`。
- 将代码获取、构建、部署、健康检查和日志查看集中管理。
- 保持部署流程简单、透明、可追踪。
- 优先满足单机自托管场景，避免过早引入集群和复杂编排。

### 1.3 目标用户

- 在 VPS 上运行个人项目的开发者。
- 管理少量内部应用的小型团队。
- 希望替代手工 SSH 部署，但不需要大型 CI/CD 系统的用户。

## 2. 产品边界

### 2.1 MVP 目标

MVP 必须跑通以下闭环：

1. 管理员完成系统初始化并登录。
2. 创建项目并配置 Git 仓库与目标分支。
3. 配置串行部署步骤、环境变量和健康检查。
4. 手动触发部署，或通过 Git Webhook 自动触发部署。
5. 系统锁定具体 Commit SHA，获取代码并依次执行步骤。
6. 用户实时查看执行日志和最终结果。
7. 用户查看部署历史，并可重新部署历史 Commit。

### 2.2 MVP 非目标

MVP 明确不支持：

- 多服务器和 SSH 远程部署。
- Kubernetes、蓝绿部署、滚动部署和预览环境。
- DAG、矩阵任务、并行步骤和分布式 Runner。
- 多环境模型以及分支到环境的映射。
- 团队、项目成员和复杂 RBAC。
- GitHub/GitLab OAuth。
- 审批流、插件系统和制品仓库。
- 针对恶意仓库或恶意命令的强隔离沙箱。
- Git LFS 和 Git Submodule。
- Windows 与 macOS 服务端运行环境。
- 完全可复现的构建与制品级回滚。

### 2.3 安全假设

mini-ci-cd 会执行项目配置的命令，因此只适用于部署受信任代码。MVP 不提供针对恶意仓库的容器级或虚拟机级安全隔离。

平台服务不得以 root 用户运行。生产部署必须使用 HTTPS，或置于提供 HTTPS 的可信反向代理之后。

## 3. 版本范围

### 3.1 MVP（0.1）

- 首次启动初始化与单管理员登录。
- Dashboard 基础统计。
- Project CRUD。
- Git HTTPS/SSH 仓库访问。
- 手动部署。
- 串行 Shell Pipeline。
- 同项目部署互斥与 FIFO 队列。
- 排队中和运行中部署取消。
- 部署步骤、实时日志与部署历史。
- 项目环境变量与敏感变量。
- HTTP 健康检查。
- GitHub、GitLab、Gitea Push Webhook。
- 自动部署。
- 历史 Commit 重新部署。
- Local Shell 部署模式。

### 3.2 第二阶段（0.2）

- Docker Compose 部署模式。
- 运行适配器（PM2、systemd）。
- Application Logs。
- 应用 Start、Stop、Restart 和 Status。
- `mini-ci-cd.yml` 仓库配置文件。
- Webhook、Email、Slack 等部署通知。
- 审计日志。

### 3.3 后续阶段

- 多用户、团队和 RBAC。
- 多环境。
- 多服务器与 SSH 远程部署。
- 构建制品与真正的快速回滚。
- CLI。
- OAuth 与 Git Provider 集成。
- Preview、Blue/Green、Rolling Deployment。
- 分布式 Runner 与插件系统。

## 4. 用户与认证

### 4.1 首次初始化

系统首次启动且不存在用户时，展示初始化页面。用户填写：

- Email。
- Username。
- Password。
- Confirm Password。

初始化成功后，该用户成为唯一 Owner，系统关闭公开注册入口。

### 4.2 登录与会话

- 使用 Email 和 Password 登录。
- 密码使用 Argon2id 哈希存储，禁止保存明文或可逆密码。
- 使用 HttpOnly、Secure、SameSite Cookie 和服务端 Session。
- 登录接口必须有频率限制。
- Session 应配置绝对有效期和空闲有效期。
- 修改密码后，除当前会话外的其他会话全部失效。
- 支持主动退出登录。

### 4.3 账户设置

Owner 可以：

- 查看 Email、Username 和创建时间。
- 修改密码。
- 注销当前会话。

MVP 不提供删除 Owner、公开注册或邀请用户功能。

## 5. Dashboard

登录后进入 Dashboard，显示：

- 项目总数。
- 当前运行中的部署数。
- 最近 24 小时成功部署数。
- 最近 24 小时失败部署数。
- 最近部署列表。

最近部署列表至少展示：

- 项目名称。
- 状态。
- Branch 和 Commit 短 SHA。
- 触发方式。
- 开始时间与耗时。

## 6. 项目管理

### 6.1 项目模型

一个 Project 在 MVP 中对应一个 Git 仓库、一个目标分支和一个部署实例。

项目包含：

- 基础信息。
- Git 仓库配置。
- Pipeline 配置。
- Local Pipeline 配置。
- 环境变量和敏感变量。
- 健康检查配置。
- Webhook 配置。
- 部署历史。

### 6.2 创建项目

必填字段：

- Project Name。
- Repository URL。
- Branch，默认 `main`。

可选字段：

- Description。
- Git 凭据。
- Pipeline Steps。
- Health Check。
- Auto Deploy，默认关闭。

### 6.3 项目标识与目录

- 系统为每个项目生成不可变 ID 和唯一 Slug。
- 工作目录由系统根据项目 ID 生成，用户不能配置任意宿主机绝对路径。
- 用户填写的相对路径经过规范化后必须仍位于项目工作目录内。
- 删除项目默认仅归档项目；物理删除工作区和日志应作为显式二次确认操作。

### 6.4 仓库连通性测试

创建或修改 Git 配置时，用户可以执行连接测试。测试结果应区分：

- 仓库不可达。
- 认证失败。
- Branch 不存在。
- Host Key 校验失败。
- 其他 Git 错误。

## 7. Git 支持

### 7.1 支持范围

MVP 支持：

- GitHub。
- GitLab。
- Gitea。
- 通用 Git HTTPS/SSH 仓库。

通用 Git 仓库支持手动部署，但只有已适配的 Provider 支持自动 Webhook 验证。

### 7.2 凭据

支持：

- HTTPS Username/Token。
- SSH Deploy Key。

要求：

- Token 和 SSH 私钥必须加密存储。
- SSH 必须校验 `known_hosts`，禁止默认关闭 Host Key 校验。
- 凭据 API 不得返回已保存的明文。
- 修改凭据采用覆盖方式，UI 不提供再次查看。

### 7.3 Checkout 规则

每次部署遵循固定流程：

1. 收到手动或 Webhook 触发请求后，先更新项目 Git 缓存仓库。
2. 在 Deployment 入队前，将目标 Branch 或 Webhook Ref 解析为完整的 40 位 Commit SHA。
3. 如果 SHA 不存在或不能从允许的目标 Branch 到达，则拒绝创建 Deployment。
4. 在同一数据库事务中保存 Commit SHA、Ref、Message、Author 和 Deployment 记录。
5. 为本次部署创建独立 Checkout，并以 detached HEAD 检出固定 SHA。
6. Checkout 完成后再次读取 `HEAD`；只有与记录的 SHA 完全一致才允许执行 Pipeline。
7. 所有 Pipeline 步骤只针对该 Checkout 执行，期间禁止 `git pull` 自动改变版本。
8. 部署结束后按保留策略清理临时工作区。

部署开始后不得再次通过 `git pull` 改变目标 Commit。

Webhook Payload 中的 Commit SHA 是目标版本的首选来源，但系统仍需 fetch 并验证对象真实存在。手动部署默认解析项目目标 Branch 的远端最新 SHA；重新部署则直接使用历史 Deployment 保存的 SHA。

Branch 在任务排队期间发生变化，不得影响已经创建的 Deployment。页面和日志必须展示最终固化的完整 SHA 与短 SHA。

## 8. Pipeline

### 8.1 执行模型

MVP 使用简单的串行 Pipeline，不支持 DAG 和并行步骤。

阶段划分为：

```text
Prepare → Checkout → Build → Deploy → Health Check
```

用户可配置 Build 和 Deploy 阶段中的命令。系统负责 Prepare、Checkout 和 Health Check。

### 8.2 Step 配置

每个用户步骤包含：

- Name。
- Command。
- Working Directory，相对于本次 Checkout 根目录。
- Timeout，默认 15 分钟。

示例：

```yaml
build:
  - name: Install dependencies
    command: pnpm install --frozen-lockfile
  - name: Build
    command: pnpm build

deploy:
  - name: Publish and restart
    command: ./scripts/deploy.sh
```

### 8.3 执行规则

- Linux MVP 固定使用一种 Shell，由服务端配置决定。
- 每个步骤在独立子进程组中执行。
- Exit Code 为 0 表示步骤成功。
- 非零 Exit Code 或执行超时表示步骤失败。
- 任一步骤失败后停止执行后续步骤。
- 超时或取消时必须终止整个子进程树。
- 每个步骤记录命令快照、状态、Exit Code、开始时间、结束时间和耗时。
- 平台服务重启后，无法恢复的运行中步骤标记为失败，并记录中断原因。

### 8.4 Runner 规则

MVP 只有一个内置 Local Runner，不提供远程 Runner 注册或选择能力。

Runner 执行契约如下：

1. Runner 领取任务前，必须确认 Deployment 仍为 `queued`，且已获得项目锁和全局并发令牌。
2. Runner 将状态原子更新为 `preparing`，记录自身实例 ID、进程 ID 和开始时间。
3. 每个 Step 使用服务端固定的 Shell 执行，不允许项目切换 Shell 可执行文件。
4. 子进程继承最小化的基础环境，只额外注入平台允许的系统变量、项目变量和部署上下文变量。
5. stdout 与 stderr 合并为有序日志流；日志记录时间、Deployment ID 和 Step ID。
6. Step 的成功条件仅为进程正常退出且 Exit Code 为 0。
7. Runner 必须同时执行 Step Timeout 和 Deployment Timeout；任一超时都会启动取消流程。
8. Runner 不自动重试用户命令，避免产生重复副作用。
9. Runner 崩溃或服务重启后，不尝试接管旧进程；对应任务在恢复检查中标记失败。
10. Runner 在所有退出路径中释放文件句柄、项目锁和全局并发令牌。

Runner 注入以下只读部署上下文变量：

```text
MINICICD_PROJECT_ID
MINICICD_DEPLOYMENT_ID
MINICICD_COMMIT_SHA
MINICICD_BRANCH
MINICICD_WORKSPACE
```

项目变量不得覆盖上述保留变量。

### 8.5 工作目录规则

数据目录由管理员在服务启动配置中指定。目录布局固定为：

```text
<data-dir>/
├── repositories/<project-id>.git/
├── workspaces/<project-id>/<deployment-id>/source/
└── logs/<project-id>/<deployment-id>.log
```

工作目录规则如下：

- `<data-dir>` 在启动时解析为绝对路径并固定，运行期间不可通过 API 修改。
- 项目和 Deployment 目录只使用系统生成的 ID，不使用项目名称拼接路径。
- 每个 Deployment 使用全新的独立 `source` 目录，禁止复用上次部署的脏工作区。
- Step Working Directory 只能是相对 `source` 根目录的路径。
- 路径在使用前必须执行清理、绝对化和符号链接解析；最终路径必须仍位于 `source` 根目录内。
- 空 Working Directory 表示 `source` 根目录。
- 拒绝绝对路径、盘符路径、UNC 路径、包含空字节的路径以及任何路径穿越结果。
- Checkout 目录由 Runner 创建，若目标目录已经存在且不为空，则任务失败，不自动覆盖未知内容。
- 清理只能删除通过 Project ID 和 Deployment ID 解析出的受管目录；清理前必须再次校验其父目录。
- Pipeline 不得修改 Git 缓存仓库；Git 缓存和部署工作区必须分离。

Local Deploy 若需要发布到工作区之外的固定目录，必须通过管理员在宿主机上预先配置的命令或脚本完成。MVP 不允许 Owner 在 UI 中指定任意绝对部署目录。

### 8.6 取消规则

- `queued` 任务取消时直接原子更新为 `cancelled`，不得被 Runner 再次领取。
- `preparing` 或 `running` 任务取消时先更新为 `cancelling`，并向 Runner 发送取消信号。
- Runner 首先向整个子进程组发送温和终止信号，并等待可配置的 Grace Period，默认 10 秒。
- Grace Period 后仍有进程存活时，强制终止整个子进程树。
- 进程树确认退出后，Deployment 才能更新为 `cancelled`。
- 如果无法确认进程树已经退出，则 Deployment 更新为 `failed`，错误原因为取消清理失败，不能错误标记为 `cancelled`。
- 进入取消流程后不得启动新的 Step 或 Health Check。
- 取消不会自动撤销已经产生的文件、数据库变更或已重启的应用。
- API 重复提交取消请求必须幂等；终态任务返回当前状态，不重复执行取消动作。
- MVP 不提供取消后的自动回滚。

## 9. Local 部署

### 9.1 支持方式与边界

MVP 只提供 Local Shell 部署模式。Build 与 Deploy 命令直接由 mini-ci-cd 服务所在宿主机的受限系统用户执行。

- 平台不推断 Node.js、Go、Python、Rust 等技术栈。
- 用户自行保证 Git、Shell、语言运行时和项目命令已安装。
- 平台只负责编排并执行用户显式配置的命令。
- MVP 不提供 PM2、systemd 等专用运行适配器。
- 用户仍可在 Pipeline 中调用宿主机已有的 PM2 或 systemd 命令，但平台不解析其应用状态和日志。

Local 模式不等于安全沙箱。所有命令均以 mini-ci-cd 服务用户的权限执行，因此服务用户必须使用最小权限，并与 root 账户隔离。

### 9.2 平台职责

平台负责：

- 执行用户配置的 Build 与 Deploy 步骤。
- 注入项目环境变量。
- 捕获命令日志和状态。
- 执行健康检查。
- 展示部署结果。
- 管理步骤超时、部署取消和子进程树清理。
- 确保命令工作目录不会越出本次部署的 Checkout 目录。

MVP 不提供通用应用进程发现、Application Logs、Start、Stop、Restart 或 Status。应用生命周期操作由用户的 Deploy 命令负责。

### 9.3 Local 安全要求

- mini-ci-cd 必须以专用非 root 系统用户运行。
- 该用户只拥有数据目录、项目工作目录和部署所需目标目录的权限。
- 不允许通过 UI 配置 `sudo` 密码或提权凭据。
- 安装文档必须列出授予 PM2、systemd 或目标目录权限所产生的风险。
- 只允许 Owner 管理项目和执行部署。

## 10. Deployment

### 10.1 触发方式

Deployment 可以由以下方式创建：

- Owner 手动触发。
- Git Push Webhook 自动触发。
- 重新部署历史 Commit。

### 10.2 状态

Deployment 状态包括：

- `queued`：等待执行。
- `preparing`：准备工作区和代码。
- `running`：执行用户步骤或健康检查。
- `succeeded`：全部步骤成功。
- `failed`：步骤失败、系统错误或健康检查失败。
- `cancelling`：正在终止进程。
- `cancelled`：已取消。
- `timed_out`：超过部署级最大时限。

每个终态都必须记录结束时间；失败状态必须保存可展示的错误摘要。

### 10.3 部署记录

每条记录至少保存：

- Deployment ID。
- Project ID。
- 触发方式和触发用户。
- Provider Delivery ID（如果存在）。
- Branch。
- Commit SHA、Message 和 Author。
- 状态与错误摘要。
- 排队、开始和结束时间。
- 各步骤执行结果。

### 10.4 部署流程

```text
Create Deployment
    ↓
Resolve Commit SHA
    ↓
Enter Project Queue
    ↓
Acquire Project Lock
    ↓
Prepare Workspace
    ↓
Checkout Fixed Commit
    ↓
Execute Build Steps
    ↓
Execute Deploy Steps
    ↓
Run Health Check
    ↓
Release Lock
    ↓
Succeeded / Failed
```

无论成功、失败、超时或取消，系统都必须释放项目锁。

## 11. 队列与并发

### 11.1 MVP 策略

- 同一项目同一时间最多有一个运行中的 Deployment。
- 同一项目的后续 Deployment 按 FIFO 排队。
- 全局最大并发部署数可由系统配置，默认值为 2。
- MVP 不支持 `Cancel Previous` 策略。
- FIFO 排序使用数据库中的 `queued_at`，时间相同时使用递增 Deployment ID。
- 项目锁和任务领取必须通过数据库原子操作完成，不能只依赖进程内内存锁。
- 一个任务只能被一个 Runner 领取；领取失败的 Runner 必须放弃执行。
- 项目锁必须记录持有它的 Deployment ID，不允许其他任务误释放。
- 只有任务进入终态并完成清理后，才释放项目锁和全局并发令牌。
- 队列调度应在新任务入队、任务结束和定时扫描时触发，不能依赖单次内存通知。
- 全局并发配置小于 1 时服务拒绝启动。

### 11.2 幂等和去重

- Webhook Delivery ID 必须唯一。
- Provider 重复投递相同事件时，不得创建重复 Deployment。
- 手动部署允许同一 Commit 重复执行。
- Webhook 创建 Deployment 时立即固化 Commit SHA，不能等出队时再读取 Branch 最新状态。

### 11.3 异常恢复

服务启动时检查非终态 Deployment：

- `queued` 可以重新进入队列。
- `preparing`、`running` 和 `cancelling` 无法可靠恢复时，标记为 `failed`。
- 错误摘要注明服务中断导致任务终止。
- 服务启动时清理已进入终态但仍残留的项目锁。
- 恢复扫描必须幂等，多次执行不能重复创建任务或破坏正在运行的新任务。
- SQLite 暂时锁定时采用有上限的退避重试；不能绕过事务直接执行任务。

## 12. 实时日志

### 12.1 日志类型

MVP 只保证 Deployment Logs，包括：

- Git 与 Checkout 日志。
- Build 日志。
- Deploy 日志。
- Health Check 日志。
- 系统错误。

Application Logs 放到第二阶段。

### 12.2 日志能力

- 实时追加显示。
- 自动滚动开关。
- 文本搜索。
- 复制可见内容。
- 下载完整日志。
- 清空当前页面显示，但不删除服务端日志。

### 12.3 存储与传输

- SQLite 只保存日志元数据，不逐行存储大体量日志。
- 每次 Deployment 使用独立的追加写入日志文件。
- 实时输出优先使用 SSE。
- 服务重启后仍可查看已写入的历史日志。
- 系统配置单次部署日志大小上限和历史保留策略。
- 超出日志上限时停止持久化后续内容，并明确标记日志已截断。

### 12.4 日志一致性与安全规则

- 每个 Deployment 只有一个服务端日志文件，文件只能追加写入。
- 每条结构化日志包含单调递增序号；SSE 客户端可使用序号断线续传。
- 日志文件必须先成功创建，Runner 才能开始执行用户命令。
- stdout 与 stderr 以 Runner 实际读取顺序写入，不承诺还原操作系统级的绝对输出顺序。
- 日志写入失败时立即停止启动新 Step；当前 Step 终止后 Deployment 标记为 `failed`。
- 日志下载接口只能按 Deployment ID 查找数据库记录，禁止接收客户端文件路径。
- 浏览器展示日志时始终按纯文本处理，不渲染其中的 HTML。
- ANSI 控制字符可用于颜色展示，但必须过滤终端控制序列和不可见危险字符。
- Secret 脱敏必须发生在写入文件和发送 SSE 之前，磁盘日志中不得先写入再二次清洗。
- 日志文件权限仅允许 mini-ci-cd 服务用户读写。
- “清空显示”只清除浏览器缓冲，不修改服务端文件；MVP 不提供单条日志人工删除。
- 达到大小上限后写入一次明确的截断标记，并继续消费子进程输出以避免管道阻塞。

## 13. 环境变量与 Secret

### 13.1 变量类型

项目变量分为：

- 普通变量：可以在 UI 中查看和修改。
- Secret：加密存储，保存后不可再次查看明文。

MVP 不引入 Development、Staging、Production 变量作用域。

### 13.2 Secret 安全规则

- 使用认证加密算法保存 Secret。
- 加密主密钥由独立环境变量或受限文件提供，不与 SQLite 数据库放在一起。
- API、审计信息和错误信息不得返回 Secret 明文。
- 日志对已知 Secret 值执行精确掩码。
- 命令预览不得展开 Secret 值。
- 文档必须说明：经过编码、分割或转换后的 Secret 无法保证被自动识别和脱敏。
- Secret 加密采用带认证的 AEAD 算法，每个值使用独立随机 Nonce。
- 密文必须保存算法版本、Nonce 和加密结果，以支持未来密钥迁移。
- 加密主密钥启动时加载到内存，缺失或长度不合法时服务拒绝启动已有 Secret 的功能。
- Secret 解密只允许发生在 Runner 构造子进程环境时，不写入临时 `.env` 文件。
- 解密后的值不得写入数据库事件、错误对象或调试日志，并尽量缩短其内存生命周期。
- 空字符串是合法 Secret 值；删除 Secret 必须使用独立删除操作，不能用空值隐式表示。
- Secret 名称不可重复且大小写敏感；修改采用创建新密文并原子替换旧密文。
- Secret 列表接口只返回名称、更新时间和 `secret: true` 标记。
- Master Key 轮换不属于 MVP，但数据格式必须为后续轮换预留 Key Version 字段。

### 13.3 注入规则

- 项目变量作为子进程环境变量注入。
- 系统保留变量名不得被覆盖。
- 变量名必须符合平台规定的环境变量格式。
- 修改变量只影响修改后创建的 Deployment。
- Deployment 创建时保存变量版本或不可变快照引用，排队期间修改项目变量不得改变已入队任务。
- 普通变量可以保存值快照；Secret 应保存加密快照或指向不可变 Secret 版本，禁止排队后读取“当前最新值”。
- 环境变量总大小超过服务端限制时拒绝创建 Deployment。

## 14. Health Check

### 14.1 配置

MVP 支持 HTTP/HTTPS 健康检查：

- Enabled。
- URL。
- Initial Delay。
- Request Timeout。
- Retry Count。
- Retry Interval。
- Expected Status Codes，默认 `200-299`。

### 14.2 执行规则

1. Deploy Steps 全部成功后等待 Initial Delay。
2. 发起健康检查请求。
3. 请求失败或状态码不符合预期时按配置重试。
4. 任意一次成功即通过。
5. 重试耗尽后 Deployment 标记为 `failed`。

MVP 健康检查失败不会自动恢复旧版本，只记录失败并保留日志。

## 15. Webhook 与自动部署

### 15.1 Provider

MVP 支持：

- GitHub Push Event。
- GitLab Push Hook。
- Gitea Push Event。

### 15.2 验证

Webhook 必须验证：

- Provider 签名或 Token。
- Project 对应的 Repository。
- Push Branch。
- 请求体大小。
- Delivery ID 是否重复。

无效请求不得创建 Deployment，并记录不含敏感信息的拒绝原因。

### 15.3 自动部署规则

- 每个项目只有一个目标 Branch。
- Auto Deploy 默认关闭。
- 只有目标 Branch 的 Push Event 可以触发部署。
- Tag、Branch Delete、Pull/Merge Request 等事件在 MVP 中忽略。
- 接口完成验证和持久化后应尽快返回，Pipeline 在后台异步执行。
- Webhook Secret 支持轮换。

## 16. 部署历史与重新部署

### 16.1 历史列表

支持按以下条件筛选：

- Status。
- Branch。
- Commit SHA。
- 时间范围。
- Trigger Type。

### 16.2 详情页

展示：

- 项目和 Deployment ID。
- 状态与错误摘要。
- Branch 和 Commit 信息。
- 触发来源。
- 排队和执行耗时。
- Step 列表。
- 实时或历史日志。

### 16.3 重新部署历史 Commit

用户可以选择历史 Deployment 的 Commit 创建一条新的 Deployment。

该能力命名为“重新部署该 Commit”，不在 MVP 中承诺为制品级回滚。由于依赖、基础镜像和外部资源可能发生变化，相同 Commit 的重新构建结果不一定完全一致。

## 17. 数据保留与清理

系统必须提供以下配置：

- 每个项目保留的 Deployment 数量。
- 日志保留天数。
- 临时 Checkout 保留策略。
- 单次部署日志大小上限。

清理任务不得删除运行中 Deployment 使用的工作区或日志。数据库记录和日志文件的删除应保持一致，并记录清理错误。

## 18. 系统配置

MVP 支持通过服务端配置设置：

- HTTP 监听地址。
- 外部访问 URL。
- 数据目录。
- Session 有效期。
- Git 与 Shell 可执行文件路径。
- 全局最大并发部署数。
- Step 默认超时与部署最大超时。
- 日志和历史保留策略。
- 加密主密钥来源。

启动时应检查 Git 和配置的 Shell 是否可用，并在系统状态页展示检查结果。项目所需语言运行时由用户自行准备，平台只在命令执行失败时报告错误。

## 19. 安全要求

### 19.1 进程与文件系统

- 服务进程不得以 root 身份运行。
- 工作目录必须由系统创建和管理。
- 所有相对路径在使用前必须规范化，并验证未越出项目目录。
- 命令必须支持超时、取消和进程树清理。
- 限制部署并发、日志大小和请求体大小。

### 19.2 Web 安全

- 所有改变状态的接口必须进行 CSRF 防护。
- Cookie 设置 HttpOnly、Secure 和合理的 SameSite 策略。
- 登录、Webhook 和敏感操作接口实施频率限制。
- API 进行输入校验并使用统一错误响应。
- 页面输出和日志展示必须防止 XSS。
- 不在客户端或日志中暴露 Session、Token、私钥和 Webhook Secret。

### 19.3 数据与备份

- SQLite 启用 WAL 模式和合理的 Busy Timeout。
- 文档提供数据库、日志和加密主密钥的备份方法。
- 必须说明：只有数据库而没有加密主密钥时，Secret 无法恢复。
- 数据库迁移需要版本化，并在升级前提示备份。

## 20. 技术架构约束

### 20.1 推荐技术栈

前端：

- React。
- TypeScript。
- Vite。
- React Router。
- TanStack Query。
- Tailwind CSS。

后端：

- Go。
- Chi。
- REST API。
- SSE 实时日志。
- SQLite。

部署依赖：

- Linux。
- Git CLI。
- Bash 或系统配置的兼容 Shell。

### 20.2 后端模块

```text
internal/
├── auth/
├── user/
├── project/
├── deployment/
├── pipeline/
├── runner/
├── webhook/
├── log/
├── health/
├── git/
├── secret/
├── local/
└── queue/
```

模块职责必须保持明确：

- Deployment 管理状态机和记录。
- Queue 管理排队、项目锁和全局并发。
- Pipeline 生成有序步骤。
- Runner 负责进程执行、超时、取消和输出。
- Git 负责缓存仓库、Commit 解析和 Checkout。
- Log 负责文件持久化、脱敏和流式输出。
- Secret 负责加解密和安全注入。
- Local 负责宿主机执行环境检查，不负责识别具体应用运行时。

### 20.3 建议数据表

```text
users
sessions
projects
project_variables
git_credentials
deployments
deployment_steps
webhook_deliveries
schema_migrations
```

MVP 暂不创建：

```text
project_members
servers
environments
audit_logs
```

## 21. 建议仓库结构

```text
mini-ci-cd/
├── apps/
│   ├── web/
│   └── server/
├── deploy/
│   ├── systemd/
│   └── install.sh
├── docs/
│   ├── requirements.md
│   ├── architecture.md
│   ├── api.md
│   └── deployment.md
├── Makefile
├── README.md
└── LICENSE
```

## 22. MVP 验收标准

满足以下全部场景后，MVP 才视为完成：

1. 新安装实例可以创建唯一 Owner，初始化后无法再次公开注册。
2. Owner 可以登录、退出和修改密码。
3. 可以创建、编辑、归档项目，并测试 Git 仓库连接。
4. 公共仓库和配置正确的私有仓库可以部署。
5. 手动部署在入队前固化并验证完整 Commit SHA；Branch 在排队期间变化不会改变目标版本。
6. Pipeline 按顺序执行，步骤非零退出后不再执行后续步骤。
7. 排队任务可以幂等取消；运行任务取消时先温和终止、超时后强制终止整个子进程树。
8. 部署期间可以实时查看 stdout 和 stderr，刷新页面后可继续查看日志。
9. Local 项目可以在隔离的部署工作区内完成构建，并通过显式 Deploy 命令发布或重启应用。
10. HTTP 健康检查成功时 Deployment 为 `succeeded`，重试耗尽时为 `failed`。
11. 同一项目的三个连续部署按照 FIFO 顺序执行，且各自使用创建时的固定 SHA。
12. 不同项目受到全局并发数限制。
13. GitHub、GitLab 或 Gitea 的有效 Push Webhook 可以创建部署。
14. 无效签名、错误仓库、错误 Branch 和重复 Delivery ID 不会创建部署。
15. 服务重启后，排队任务可以恢复；被中断的运行任务有明确终态和错误原因。
16. Secret 不出现在读取 API、命令预览、SSE 和磁盘日志中；排队后修改 Secret 不影响已入队任务。
17. 可以筛选部署历史、查看步骤详情并下载日志。
18. 可以基于历史 Commit 创建新的重新部署任务。
19. 绝对路径、路径穿越和解析后越出 Checkout 根目录的符号链接路径会被拒绝。
20. 日志具有递增序号并支持 SSE 断线续传；达到上限后标记截断且不会阻塞子进程。
21. 日志和临时工作区按配置执行清理，且不会影响运行中的任务。
22. 两个 Runner 竞争同一任务时，只有一个能够通过数据库原子操作领取并执行。
23. Runner 或服务异常退出后，遗留任务和项目锁能够按恢复规则收敛到一致状态。

## 23. 推荐开发顺序

1. 项目骨架、配置系统、数据库迁移和系统自检。
2. Owner 初始化、登录和 Session。
3. Project CRUD、Secret 和 Git 凭据。
4. Git 缓存、Commit 解析与独立 Checkout。
5. Deployment 状态机、Queue 和项目锁。
6. Runner、Step 超时、进程树终止和日志文件。
7. SSE 实时日志与部署详情页。
8. Local Pipeline 与部署命令。
9. HTTP Health Check。
10. Webhook 验证、幂等和自动部署。
11. Dashboard、部署历史与重新部署。
12. 数据清理、异常恢复、安全测试和安装文档。

## 24. 产品体验目标

用户首次使用时，应能在一个连续流程中完成：

```text
初始化 Owner
    ↓
创建项目
    ↓
填写 Git 仓库与 Branch
    ↓
测试仓库连接
    ↓
配置 Build / Deploy Steps
    ↓
配置环境变量与健康检查
    ↓
手动部署并查看实时日志
    ↓
配置 Webhook
    ↓
后续通过 git push 自动部署
```

产品设计应始终围绕这一条主路径，新增能力不能显著增加初次部署的理解成本。
