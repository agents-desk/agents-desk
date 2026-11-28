<!-- intent-skills:start -->
## Skill Loading

Use the repository’s installed Intent. If it is unavailable, report the missing dependency instead of downloading a replacement.
Before editing files for a substantial task:
- Run `bunx --no-install --package @tanstack/intent intent list` from the workspace root to see available local skills.
- If a listed skill matches the task, run `bunx --no-install --package @tanstack/intent intent load <package>#<skill>` before changing files.
- Use the loaded `SKILL.md` guidance while making the change.
- Monorepos: when working across packages, run the skill check from the workspace root and prefer the local skill for the package being changed.
- Multiple matches: prefer the most specific local skill for the package or concern you are changing; load additional skills only when the task spans multiple packages or concerns.
<!-- intent-skills:end -->

# agents-desk

agents-desk 是团队的综合服务平台，以 AI 为核心，以聊天为基础。已有功能设计见 [docs/产品设计.md](docs/产品设计.md)，后续产品与开发规划见 [docs/后期产品计划.md](docs/后期产品计划.md) 和 [docs/后期开发计划.md](docs/后期开发计划.md)。项目是 Go 写的后端加 TanStack Start 写的前端，整个应用编译成一个二进制文件。本文写给在这个项目中工作的人和 AI 编程助手：怎样运行、提交前检查什么、代码按什么约定写。

## 应用

- 框架是 `github.com/go-tenon/tenon`。框架的通用约定（文档、提交前检查、测试和代码约定）在本文末尾的托管区块中，`bun run agents` 按 `go.mod` 中的框架版本更新它；前面各节是 agents-desk 自己的约定。
- 项目的前端、客户端和安装的技能用 MCP 工具 `tenon_app_info` 查看，也可以直接看 `tenon.json` 和 `apps/`：`apps/admin` 是管理员端（`/admin/`），`apps/web` 是工作区端（`/`），`apps/widget` 是网页聊天插件的聊天窗口（`/widget/`，嵌入产品网站和 App，`public/loader.js` 是嵌入脚本，开发时 `/widget/demo` 是演示页），都只在浏览器中渲染。
- 应用的入口是 `./cmd/agents-desk`，`go run ./cmd/agents-desk --help` 列出应用的命令。

## 开发环境

- Go，版本以 [go.mod](go.mod) 为准；
- [Bun](https://bun.sh)，安装前端依赖、运行前端的开发服务器；
- Docker，仅用于开发和测试期：全机共享常驻 PostgreSQL、NATS 等依赖，各项目与 `git worktree` 通过 `.env` 和 `config/local.yaml` 隔离（设置 `TENON_WT_SETUP` 时 `bun run dev` 自动初始化）。

第一次运行：

```bash
printf 'crypt:\n  key: %s\n' "$(openssl rand -base64 32)" > config/local.yaml   # 应用密钥，只在本机（或写入 .env 的 APP_KEY）
go run ./cmd/agents-desk migrate up
go run ./cmd/agents-desk seed                                        # 开发用的数据：平台管理员 admin@example.com、示例工作区 demo（主管理员 owner@、管理员 manager@、坐席 agent@example.com，团队收件箱“一线客服”，测试渠道和 10 个示例会话），密码都是 password
bun run dev                                                          # 管理员端在 http://localhost:8081/admin/
```

## 常用命令

```bash
bun run dev                       # 运行应用和前端的开发服务器，改代码后自动重新编译；打开 http://localhost:8081
bun run generate                  # 接口变了以后重新生成前端的客户端（dev 会自动运行）
bun run build                     # 构建可执行文件 bin/agents-desk，前端打包在其中
bun run typecheck                 # 前端的类型检查
bun run check                     # 静态检查约定：写死的文字和颜色、数据库时钟、错误码的译文、前端依赖的版本
bun run agents                    # 升级框架之后，更新框架文档、AGENTS.md 的托管区块、技能和 MCP 配置
go run ./cmd/agents-desk migrate up   # 执行迁移；migrate make <名称> 新建迁移，migrate publish 发布框架的迁移，migrate verify 检查框架的迁移
go run ./cmd/agents-desk seed       # 写入开发和演示用的账号、工作区和示例会话，可以重复运行；只在开发和演示环境中运行
go run ./cmd/agents-desk dev message --from rahul "mera order nahi aaya"   # 以客户的身份往 demo 的测试渠道发一条消息
go run ./cmd/agents-desk admin create --email <邮箱>   # 创建平台管理员，密码在终端中输入（生产环境用它）
go run ./cmd/agents-desk routes     # 全部路由
go run ./cmd/agents-desk openapi    # OpenAPI 文档
go run ./cmd/agents-desk --help     # 应用的其他命令
```

加前端用 `go tool tenon add frontend <名称>`，加桌面端用 `go tool tenon add client <名称>`，见框架文档的 `installation/tooling.md`。

## 目录

```
cmd/agents-desk/         入口：main.go 是配置和命令，app.go 装配依赖和运行部分，http.go 登记路由
internal/<模块>/         业务代码，一个业务动作一个文件
internal/apptest/        测试共用的装配：临时库、账号体系、应用密钥
migrations/              SQL 迁移，包括 migrate publish 发布的框架迁移
locales/                 翻译文件，简体中文和英文：服务端的 <语言>.yaml（错误提示、资源和字段、枚举和权限的显示文字），前端的 <前端>.<语言>.yaml
docs/                    产品设计（已有功能）与后期产品、开发计划
config/app.yaml          配置；本机的覆盖写在 config/local.yaml，不提交，命令和 bun run dev 自动叠加它
apps/<前端>/             TanStack Start 前端，挂载路径写在 package.json 的 tenon 字段
packages/client/         bun run generate 生成的客户端，不手改
tenon/                   tenon 同步的框架前端包与文档（随仓库提交，不手改）
```

## 测试数据库

依赖数据库的测试用 `internal/apptest` 的 `New`（其中是 `database/dbtest` 的 `Open`）为每个测试建一个临时库，直接读取环境中的 `POSTGRES_TEST_URL`（指向本机共享常驻测试 PostgreSQL）。

## 依赖的文档

写到依赖的地方时，先查它们给 AI 的文档。版本以 `go.mod` 和 `apps/web/package.json` 为准；网上的文档是最新的版本，两者有出入时以项目中的版本为准。

- Go 的依赖：MCP 服务 `gopls`（查找符号、读包的 API、检查编译错误），或者 `go doc <包>.<名称>`。
- Mantine：MCP 服务 `mantine`（组件的属性和用法）、技能 `mantine-form`、`mantine-combobox`、`mantine-custom-components`，文档目录 <https://mantine.dev/llms.txt>。
- TanStack：Router 随包发布的技能经 TanStack Intent 加载，见本文开头 `intent-skills` 一段，允许的包写在 `package.json` 的 `intent.skills` 中；文档目录 <https://tanstack.com/router/latest/llms.txt>、<https://tanstack.com/start/latest/llms.txt>、<https://tanstack.com/query/latest/llms.txt>。
- React：<https://react.dev/llms.txt>；Vite：<https://vite.dev/llms.txt>；Bun：<https://bun.com/llms.txt>。

依赖的写法与本文、`tenon/docs` 冲突时，以本文和 `tenon/docs` 为准：例如业务逻辑写在 Go 的接口中，前端只负责展示和交互。

## 代码约定

### 工作区和账号

- 工作区在代码中统一叫 tenant，与 Tenon 的租户一致：表 `tenants`、列 `tenant_id`、包 `internal/tenant`、权限 `tenants:*`。给用户看的文字叫“工作区”。
- 工作区的 ID 是 UUIDv7，作为租户、authz 的域和业务表的 `tenant_id`，创建后保持不变；地址用 slug（`/t/{slug}/`），只在租户中间件中由 slug 换成 ID。工作区的链接由 `tenant.Addresses` 生成。
- 属于工作区的表都有 `tenant_id`，唯一约束和常用索引以它开头；每个查询都带上 `tenant_id = sub.Tenant`，每个业务动作都有“其他工作区访问不到”的测试。
- 团队收件箱在代码中统一叫 team：表 `teams`、列 `team_id`、包 `internal/team`、权限 `teams:*`。给用户看的文字叫“团队收件箱”；代码中的 inbox 留给收件箱模块（我的、@我、未指派、全部、视图）。
- 平台管理员和工作区成员是两套账号：各自一张表、一个 `identity.SubjectType`、一个 `login.Realm`，会话 cookie 分别是 `__Host-admin` 和 `__Host-session`。成员账号是全局的，在每个工作区中的身份由角色决定。

### 实时推送

- 会话的写入都经过 `commit`，它在提交后把变化推送给修改前后能看到会话的成员。其他要通知成员的写入用 `tx.AfterCommit` 调用 `push.Send`，推送到成员的个人频道（`internal/push`）。推送只带 ID，页面收到后经接口重新读取。

### 界面文字

- 界面文字写在 `locales/web.<语言>.yaml`、`locales/admin.<语言>.yaml` 中，页面用 `useT` 取出，菜单标题写消息 ID，见框架文档的 `frontend/i18n.md`。应用在每个前端中的名称是 `app.<前端>`。各前端的 `i18n` 在 `src/i18n.ts` 中。消息 ID 按模块起名（`inbox.empty`），不用 `tenant.`、`team.`、`member.`、`role.` 这些服务端已经在用的前缀。界面的语言跟随成员在偏好设置中选的语言，有简体中文（`zh-CN`）和英文（`en`）；成员选了其他语言时显示简体中文。
- 加一条消息时，简体中文和英文的翻译文件都写上它，`locales` 的测试检查每种语言的消息 ID 相同。

<!-- tenon:begin -->
<!-- 这一段由 go tool tenon agents 按框架版本维护，不要手改。 -->

## 框架与文档

- 框架版本以 `go.mod` 中的 `github.com/go-tenon/tenon` 为准，对应文档在 `tenon/docs`、前端包源码在 `tenon/js`，由 `bun run agents` / `bun run generate` 同步并提交。先读目录 `tenon/docs/README.md`，写代码前读对应页面。
- 查文档可用 MCP 工具 `tenon_search_docs`、`tenon_read_doc`，也可直接搜索阅读 `tenon/docs` 与 `tenon/js`。项目结构用 `tenon_app_info` 查看，改完代码用 `tenon_check` 检查约定与框架装配。
- Go 依赖的签名和说明用 `go doc` 读与 `go.mod` 一致的版本；Mantine、TanStack 等依赖的 AI 资源按 `README.md` 的“AI 编程助手”一节接入，与 Tenon 冲突时以本文和 `tenon/docs` 为准。

## 开发与部署约定

- Docker 仅用于开发和测试期：全机共享常驻 PostgreSQL、NATS、Redis、S3 等依赖；多项目与 `git worktree` 按库名、S3 桶、Redis DB、队列 stream/前缀和 `config/local.yaml` 的 `http.addr` 隔离（设置 `TENON_WT_SETUP` 时 `bun run dev` 自动初始化）。测试直接使用环境中的 `POSTGRES_TEST_URL` 等变量；容器启停与 `compose*.yml` 仅在用户明确要求时执行，依赖不可达时停止并报告。
- 生产部署采用原生单二进制加 systemd 零停机升级：用 `go tool tenon add deploy` 生成 `deploy/app.yaml`、`deploy/example/` 与 `deploy/targets/` 骨架，用 `go tool tenon deploy` 初始化主机、按实例发布与回滚，见 `tenon/docs/installation/deployment.md`。

## 提交前

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
bun run typecheck
bun run check
bun run generate && git diff --exit-code
```

CI（`.github/workflows/ci.yml`）运行同样的检查并构建一次；生成或同步的文件（`tenon/`、客户端、路由树、界面文字、技能、本节约定）未提交时失败。

## 编写测试

- 顶层测试第一行调用 `t.Parallel()`；修改全局状态（`t.Setenv`、`slog.SetDefault` 等）的测试串行运行。
- 依赖数据库的测试用 `database/dbtest` 的 `Open` 为每个测试建临时库；外部服务尽量在进程内模拟（S3 用 [gofakes3](https://github.com/johannesboyne/gofakes3)，HTTP API 用 `httptest`）。
- 时间相关逻辑用 `testing/synctest` 控制时间。SQL 中的时间由 Go 算出后作为参数传入，不用 `NOW()` 等数据库时钟函数。

## 代码约定

### 业务动作

- 一个用例写成一个业务动作，放在 `internal/<模块>/`，一个动作一个文件。HTTP、命令、队列、MCP 等入口只做输入转换并调用 `Handle`。
- 业务规则、权限判断和校验写在 Go 中；前端和 App 只负责展示和交互。
- 业务错误用 `errorx.Define(kind, code, "源语言提示")` 定义稳定错误码与源语言提示，其余语言的译文写在 `locales/<语言>.yaml` 中。

### 数据与事务

- 迁移中的每张表和每个字段都要写 `COMMENT ON TABLE` / `COMMENT ON COLUMN`，迁移测试调用 `dbtest.CheckSchema(t, db)` 检查时间列和表/字段注释。
- 多条写入需原子成功时放进 `database.RunInTx`；队列任务在同一个事务中发布；事务内复用框架存储用 `.WithDB(tx)`，避免重造实例丢弃缓存与凭据复查选项。
- 回滚撤销不了的副作用（删缓存、广播）用 `tx.AfterCommit` 登记，提交后再执行。
- 缓存数据不带权限：命中缓存后仍须检查访问权限。

### 注释

- 包、类型、函数（含非导出和接口方法）都写注释，直述它是什么、做什么、返回什么；字段含义写在行尾；长函数按步骤加简短注释。

### 函数

- 保持完整阅读流（单函数可到 100–200 行），只在复用或职责独立时抽函数。
- 直接调用标准库和社区组件原生类型（`*bun.DB`、`*slog.Logger`、`*cobra.Command`），不再包一层。
- 依赖全部显式传参，包级变量只放只读值。存储接收 `bun.IDB`，自开事务的服务接收 `*bun.DB`。

### 错误

尽早暴露失败和错误：出错即返回，不加不会发生的防御检查，不吞错误，不用默认值掩盖。

### 前端

- 用 Mantine 和 `@go-tenon/ui` 组件，用生成的客户端和 TanStack Query 调接口，路由用 TanStack Router 文件路由。
- 颜色从 Mantine 主题取：默认品牌色 `brand`，彩色写预设名（`deep-orange`、`var(--mantine-color-deep-orange-6)`），中性色用 `gray`/`dark`/`c="dimmed"`/`--tn-*`，不写十六进制或 `rgb()`。
- 状态色固定：成功 `green`，进行中 `bright-blue`，等待 `yellow`，注意 `deep-orange`，失败 `red`，无状态 `gray`。
- 界面文字写在 `locales/<前端>.<语言>.yaml`，页面用 `useT`，菜单标题写消息 ID，见 `tenon/docs/frontend/i18n.md`。
- 生成或同步的代码（`tenon/`、`packages/client`、`routeTree.gen.ts`、`messages.gen.ts`）不手改。

### 接口

- 改接口后运行 `bun run generate` 并与接口一起提交。
- 每个接口都能用 `Authorization: Bearer` 调用；浏览器用会话 cookie 携带同一 token。
- 前端所需元数据（校验、权限、枚举）写在 Go 接口定义中，经 OpenAPI 生成给客户端。
<!-- tenon:end -->
