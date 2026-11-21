# agents-desk

团队的综合服务平台：以 AI 为核心，以聊天为基础。对客户提供专业的客服，对员工提供内部聊天和企业微信、钉钉、飞书、Slack 等平台的接入，对合作商家提供沟通和自动化服务。

- [产品设计](docs/产品设计.md)：产品是什么、由哪些部分组成、主要流程和整体架构。
- [开发计划](docs/开发计划.md)：分阶段的计划项，以及它们之间的依赖。

## 开发

### 开始

需要 Go（版本见 [go.mod](go.mod)）、[Bun](https://bun.sh) 和 Docker：

```bash
bun install
docker compose up -d --wait      # PostgreSQL 和 NATS
printf 'crypt:\n  key: %s\n' "$(openssl rand -base64 32)" > config/local.yaml   # 应用密钥，只在本机
go run ./cmd/agents-desk migrate up
go run ./cmd/agents-desk seed    # 开发用的账号和示例工作区，密码都是 password
bun run dev      # 工作区端在 http://localhost:8081，管理员端在 /admin/，接口文档在 /api/docs、/admin/api/docs
bun run build    # 构建 bin/agents-desk
```

用 `admin@example.com` / `password` 登录管理员端，可以看到示例工作区 `demo`；它的主管理员 `owner@example.com` / `password` 登录工作区端 <http://localhost:8081/>，进入 `/t/demo/`。示例工作区中还有管理员 `manager@example.com`、坐席 `agent@example.com`，密码都是 `password`，用来看不同角色的界面；他们两人在团队收件箱“一线客服”中，新会话在两人之间轮流分配。示例工作区还有一个测试渠道和 10 个示例会话；`go run ./cmd/agents-desk dev message --from rahul "..."` 以客户的身份往测试渠道发一条消息，用来看新消息进来的样子。生产环境不运行 `seed`，第一个平台管理员用 `admin create --email <邮箱>` 创建，密码在终端中输入。

上传的文件（头像等）默认保存在本机的 `storage/files` 目录中（不提交），由应用在 `/storage/` 下提供访问。部署多台服务器或使用 CDN 时改用 S3 兼容的对象存储：在 `config/app.yaml` 的 `filesystem.disks` 中配置 `s3`，并把 `default_disk` 设为 `s3`（或设置环境变量 `FILES_DISK=s3`），见 `.tenon/docs/libraries/filesystem.md`。

目录、常用命令、提交前的检查和代码约定见 [AGENTS.md](AGENTS.md)，框架的文档在 `.tenon/docs` 中（`bun run dev` 时生成），也可以在线阅读 [Tenon 文档](https://github.com/go-tenon/tenon/tree/main/docs)。

### AI 编程助手

#### Tenon 自动接入的

`tenon init` 和 `bun run agents`（`go tool tenon agents`）为 Claude Code、Codex、Cursor、Antigravity 准备好了这些，升级框架后运行 `bun run agents` 更新（`AGENTS.md` 由项目自己维护）：

- `.tenon/docs`：与 `go.mod` 中的框架版本一致的文档；
- `tenon-*` 技能：新增业务动作、写接口、加表、写页面等常做的事的步骤；
- MCP 服务 `tenon`（`go tool tenon mcp`）：检索框架的文档、查看项目信息、读 `bun run dev` 的输出。

选择接入哪些助手见 `.tenon/docs/installation/ai-assistants.md#choosing-assistants`。

#### 依赖的官方方案

项目用到的 Go、Mantine、TanStack 等也为 AI 编程助手提供了文档、MCP 服务和技能，按它们的官方方法接入。它们的写法与 Tenon 的约定冲突时，以 `AGENTS.md` 和 `.tenon/docs` 为准。

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
