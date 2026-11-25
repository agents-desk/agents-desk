# agents-desk

团队的综合服务平台：以 AI 为核心，以聊天为基础。对客户提供专业的客服，对员工提供内部聊天和企业微信、钉钉、飞书、Slack 等平台的接入，对合作商家提供沟通和自动化服务。

- [产品设计](docs/产品设计.md)：产品是什么、由哪些部分组成、主要流程和整体架构。
- [开发计划](docs/开发计划.md)：分阶段的计划项，以及它们之间的依赖。

## 开发

### 开始

需要 Go（版本见 [go.mod](go.mod)）、[Bun](https://bun.sh)，以及本机共享常驻的 PostgreSQL 18+ 与开启 JetStream 的 NATS 2.15+（默认连接 `127.0.0.1:5432` 与 `127.0.0.1:4222`，通过 `.env` 中的 `DATABASE_URL`、`NATS_URL`、`QUEUE_STREAM` 覆盖；配置了 `TENON_WT_SETUP` 时 `bun run dev` 自动初始化 `.env` 与 `config/local.yaml`）：

```bash
bun install
printf 'crypt:\n  key: %s\n' "$(openssl rand -base64 32)" > config/local.yaml   # 应用密钥，只在本机（或写入 .env 的 APP_KEY）
go run ./cmd/agents-desk migrate up
go run ./cmd/agents-desk seed    # 开发用的账号和示例工作区，密码都是 password
bun run dev      # 工作区端在 http://localhost:8081，管理员端在 /admin/，接口文档在 /api/docs、/admin/api/docs
bun run build    # 构建 bin/agents-desk
```

用 `admin@example.com` / `password` 登录管理员端，可以看到示例工作区 `demo`；它的主管理员 `owner@example.com` / `password` 登录工作区端 <http://localhost:8081/>，进入 `/t/demo/`。示例工作区中还有管理员 `manager@example.com`、坐席 `agent@example.com`，密码都是 `password`，用来看不同角色的界面；他们两人在团队收件箱“一线客服”中，新会话在两人之间轮流分配。示例工作区还有一个测试渠道和 10 个示例会话；`go run ./cmd/agents-desk dev message --from rahul "..."` 以客户的身份往测试渠道发一条消息，用来看新消息进来的样子。生产环境不运行 `seed`，第一个平台管理员用 `admin create --email <邮箱>` 创建，密码在终端中输入。

上传的文件（头像等）默认保存在本机的 `storage/files` 目录中（不提交），由应用在 `/storage/` 下提供访问。部署多台服务器或使用 CDN 时改用 S3 兼容的对象存储：在配置文件的 `filesystem.disks` 中配置 `s3`，并把 `default_disk` 设为 `s3`（或设置环境变量 `FILES_DISK=s3`），见 `tenon/docs/libraries/filesystem.md`。

目录、常用命令、提交前的检查和代码约定见 [AGENTS.md](AGENTS.md)，框架的文档在 `tenon/docs`、前端包源码在 `tenon/js`，也可以在线阅读 [Tenon 文档](https://github.com/go-tenon/tenon/tree/main/docs)。

<!-- tenon:deploy:begin -->
<!-- 这一段由 go tool tenon agents 按框架版本维护；如需按项目自定义，删掉首尾标记即可。 -->

## 生产部署（Ubuntu + 原生二进制 + systemd + Nginx）

### 前提条件

- **应用服务器**：Ubuntu（systemd），本地可通过 SSH 登录目标用户并具备免密 `sudo` 权限，已安装 Nginx。
- **基础设施**：按项目实际依赖准备外部服务（例如 PostgreSQL 18+ 放行应用服务器 IP 白名单，NATS 2.15+ 开启 JetStream `-js` 并放通 `4222` 端口）。
- **本地构建环境**：已安装与 [go.mod](go.mod) 一致的 Go 和 Bun。

### 1. 准备部署与生产配置

首次生成 `deploy.yaml`（留空 `arch` 时 `tenon deploy` 会自动通过 SSH 探测目标服务器是 `amd64` 还是 `arm64` 并交叉编译）：

```bash
go tool tenon add deploy --user <ssh用户> --host <服务器IP>
```

在 `deploy.yaml` 中将生产配置设为单一独立文件 `config_files: [config/prod.yaml]`，并创建本地 `config/prod.yaml`（已在 `.gitignore` 中，不提交到仓库）。未显式填写的超时、健康探针过滤（`/livez`、`/readyz`）等项均由框架默认值补全，只需填写生产特有配置：

```yaml
http:
  addr: "127.0.0.1:8080"                # 仅监听本机回环，由同机 Nginx 反向代理转发
  public_url: "https://example.com"     # 应用对外的访问地址，按需填写
  client_ip:
    trusted_proxies:
      - "127.0.0.1/32"                  # 同机 Nginx 反代网段，用于还原客户端真实 IP
  access_log:
    disabled: true                      # 反向代理已记录访问日志时可关闭应用访问日志；也可改为 logs.levels.http: warn 只记 4xx/5xx

# 按项目实际配置结构填写依赖项（仅保留项目 Config 中声明的字段；密码含 @ 等特殊字符需 URL 编码，例如 @ 写为 %40）
# logs:
#   level: info
# telemetry:
#   service: agents-desk
#   endpoint: https://o2.example.com/api/default
#   headers:
#     Authorization: Basic <base64(user:password)>
#   browser:
#     site: https://o2.example.com
#     client_token: <openobserve-rum-client-token>
#     organization: default
#     environment: production
# database:
#   dsn: postgres://<db_user>:<db_pass>@<db_host>:5432/<db_name>?sslmode=disable
# crypt:
#   key: "<openssl rand -base64 32 生成的密钥>"
# nats:
#   url: nats://<nats_user>:<nats_pass>@<nats_host>:4222
# filesystem:
#   disks:
#     local:
#       root: /opt/agents-desk/storage/files   # 生产本地磁盘使用绝对路径，目录归属 agents-desk 服务用户
```

本地校验配置与预演发布计划：

```bash
go run ./cmd/agents-desk config check --config config/prod.yaml
go tool tenon deploy --dry-run
```

### 2. 初始化目标服务器

首次部署运行一次 `setup`，自动检查环境、创建 `agents-desk` 系统用户与 `/opt/agents-desk` 目录，写入 `/etc/systemd/system/agents-desk@.service`，并在 `/usr/local/bin/agents-desk` 安装运维入口（后续调整 `deploy.yaml` 中的 `config_files` 列表后需重新执行 `setup` 并重启服务）：

```bash
go tool tenon deploy setup
```

### 3. 打包、上传并启动服务

直接运行 `go tool tenon deploy`，自动通过 SSH 探测目标机架构（`amd64` 或 `arm64`，多机校验一致）、本地交叉编译嵌入前端与迁移文件、上传校验，并依次执行远端 `config check`、`migrate up`、`doctor` 与零停机启动/热重载：

```bash
# 推荐：自动探测架构、交叉编译并发布
go tool tenon deploy

# 可选：手动交叉编译后发布指定二进制（适合 CI 构建与发布分离）
bun run build --arch amd64              # 或 --arch arm64
go tool tenon deploy --binary bin/agents-desk
```

验证服务状态与远端自检：

```bash
go tool tenon deploy status
go tool tenon deploy run -- doctor
```

### 4. 配置 Nginx 反向代理（80 端口模板）

在服务器上创建或更新站点配置（例如 `/etc/nginx/conf.d/agents-desk.conf`），将 80 端口请求转发到本机 `127.0.0.1:8080`：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close; # 普通 HTTP 走同机回环短连接（配合热重载避免 502），WebSocket 自动升级
}

upstream agents-desk_backend {
    server 127.0.0.1:8080 max_fails=3 fail_timeout=10s;
}

server {
    listen 80;
    server_name example.com;

    client_max_body_size 32m;

    location / {
        proxy_pass http://agents-desk_backend;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Request-ID $request_id;

        proxy_buffering off;       # 流式输出、SSE 与 WebSocket 实时透传
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_next_upstream error timeout invalid_header http_502 http_503 http_504;
    }
}
```

> HTTPS（443 端口、TLS 证书、HTTP/2 与 80→443 重定向）由运维人员按需在 Nginx 或前置负载均衡上配置；浏览器会话 Cookie 带有 `__Host-` 与 `Secure` 属性，外网访问需通过 HTTPS 接入。

校验、重载 Nginx 并验证就绪探针：

```bash
sudo nginx -t && sudo systemctl reload nginx
curl -fsS http://127.0.0.1:8080/readyz
```

### 5. 日常运维与一次性命令

```bash
go tool tenon deploy               # 发布新版本或同步修改后的 config/prod.yaml（零停机热重载）
go tool tenon deploy status        # 查看当前发布版本与 systemd 服务状态
go tool tenon deploy logs -F       # 实时查看远端应用日志
go tool tenon deploy rollback      # 回退二进制与配置到上一版本
```

`tenon deploy setup` 已将运维入口安装到 `/usr/local/bin/agents-desk`，自动挂载服务用户、工作目录、环境变量与默认配置清单（无需手写 `--config`）；登录服务器后可在任意目录直接执行应用子命令，或在本地通过 `ssh -t` 交互执行（例如创建首个后台账号；终端输入密码时关闭回显属正常现象）：

```bash
# 登录服务器后直接运行
agents-desk doctor
agents-desk <子命令>

# 或在本地通过 SSH 交互运行
ssh -t <ssh用户>@<服务器IP> agents-desk <子命令>
```

多机 `roles` 拓扑、维护窗口与其他部署选项见 `tenon/docs/installation/deployment.md`。
<!-- tenon:deploy:end -->

### AI 编程助手

#### Tenon 自动接入的

`tenon init` 和 `bun run agents`（`go tool tenon agents`）为 Claude Code、Codex、Cursor、Antigravity 准备好了这些，升级框架后运行 `bun run agents` 更新（`AGENTS.md` 中的托管区块与 `README.md` 中的部署区块同步更新）：

- `tenon/docs`：与 `go.mod` 中的框架版本一致的文档；
- `tenon-*` 技能：新增业务动作、写接口、加表、写页面等常做的事的步骤；
- MCP 服务 `tenon`（`go tool tenon mcp`）：检索框架的文档、查看项目信息、读 `bun run dev` 的输出。

选择接入哪些助手见 `tenon/docs/installation/ai-assistants.md#choosing-assistants`。

#### 依赖的官方方案

项目用到的 Go、Mantine、TanStack 等也为 AI 编程助手提供了文档、MCP 服务和技能，按它们的官方方法接入。它们的写法与 Tenon 的约定冲突时，以 `AGENTS.md` 和 `tenon/docs` 为准。

这个项目已经按下面的方法接上了 gopls、Mantine 的 MCP 服务和技能，以及 TanStack Router 的技能，配置都已提交；更新依赖的技能时重新执行对应的命令。

下面以 Claude Code 和 Codex 的命令为例。Claude Code 的命令加上 `-s project` 时写进项目的 `.mcp.json`，提交后团队共用。Cursor（`.cursor/mcp.json`）、Antigravity（`.agents/mcp_config.json`）在 `mcpServers` 中加上同样的 `command` 和 `args`，VS Code（`.vscode/mcp.json`）写在 `servers` 中。`bun run agents` 只改写其中的 `tenon` 一项，你加上的服务保持原样。

**Go：gopls**（[官方说明](https://go.dev/gopls/features/mcp)）。Go 的语言服务，在项目和依赖（与 `go.mod` 的版本一致）中查找符号、读包的 API、检查编译错误：

```bash
go install golang.org/x/tools/gopls@latest
claude mcp add gopls -- gopls mcp
codex mcp add gopls -- gopls mcp
gopls mcp -instructions    # 输出给模型看的使用说明，可以放进助手的上下文
```

Go 的其他依赖（Huma、chi、Bun ORM、goose 等）用 `go doc` 读与 `go.mod` 一致的版本，例如 `go doc github.com/danielgtaylor/huma/v2.Register`。

**Mantine**（[官方说明](https://mantine.dev/guides/llms/)）。文档目录 <https://mantine.dev/llms.txt>；MCP 服务查组件的属性和用法：

```bash
claude mcp add mantine -- npx -y @mantine/mcp-server
codex mcp add mantine -- npx -y @mantine/mcp-server
```

需要时安装 Mantine 的技能，例如表单、自定义选择框：

```bash
npx skills add https://github.com/mantinedev/skills --skill mantine-form
npx skills add https://github.com/mantinedev/skills --skill mantine-combobox
```

**TanStack Router、Start、Query**。文档目录：<https://tanstack.com/router/latest/llms.txt>、<https://tanstack.com/start/latest/llms.txt>、<https://tanstack.com/query/latest/llms.txt>。Router 和 Start 的包中带有技能，版本与安装的包一致，用 [TanStack Intent](https://tanstack.com/intent/latest) 接入（在终端中交互选择）：

```bash
bunx @tanstack/intent@latest install
```

选择时只启用 `@tanstack/react-router`、`@tanstack/router-core` 的技能：这个项目的业务逻辑都在 Go 的接口中，前端只负责展示和交互。

**React、Vite、Bun**。文档目录：<https://react.dev/llms.txt>、<https://vite.dev/llms.txt>、<https://bun.com/llms.txt>。
