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

agents-desk 是团队的综合服务平台，以 AI 为核心，以聊天为基础。产品设计见 [docs/产品设计.md](docs/产品设计.md)，开发计划见 [docs/开发计划.md](docs/开发计划.md)。项目是 Go 写的后端加 TanStack Start 写的前端，整个应用编译成一个二进制文件。本文写给在这个项目中工作的人和 AI 编程助手：怎样运行、提交前检查什么、代码按什么约定写。

## 框架与文档

- 框架是 `github.com/go-tenon/tenon`，版本以 `go.mod` 为准。这个版本的文档在 `.tenon/docs` 中，`bun run dev` 和 `bun run agents` 会更新它。先读目录 `.tenon/docs/README.md`，写某一部分的代码前读对应的页面，写法以这个版本的文档为准。
- 查框架的文档用 MCP 工具 `tenon_search_docs`、`tenon_read_doc`；用 rg 搜索时指定目录，例如 `rg 迁移 .tenon/docs`：`.tenon` 被 .gitignore 忽略，在项目根目录搜索时不包含它。
- 项目的前端、客户端和安装的技能用 MCP 工具 `tenon_app_info` 查看，也可以直接看 `tenon.json` 和 `apps/`：`apps/admin` 是管理员端（`/admin/`），`apps/web` 是工作区端（`/`），都只在浏览器中渲染。
- 应用的入口是 `./cmd/agents-desk`，`go run ./cmd/agents-desk --help` 列出应用的命令。
- 依赖的版本以 `go.mod` 和各前端的 `package.json` 为准。Go 的依赖用 `go doc` 读与 `go.mod` 一致的版本，例如 `go doc github.com/danielgtaylor/huma/v2.Register`。
- Mantine、TanStack 等依赖给 AI 的文档、MCP 服务和技能按它们的官方方案接入，见 `README.md` 的“AI 编程助手”一节；它们的写法与 Tenon 的约定冲突时，以本文和 `.tenon/docs` 为准。

## 开发环境

- Go，版本以 [go.mod](go.mod) 为准；
- [Bun](https://bun.sh)，安装前端依赖、运行前端的开发服务器；
- Docker，在本机启动 PostgreSQL 和 NATS。

第一次运行：

```bash
docker compose up -d --wait                                          # PostgreSQL（127.0.0.1:15432）和 NATS（127.0.0.1:14222）
printf 'crypt:\n  key: %s\n' "$(openssl rand -base64 32)" > config/local.yaml   # 应用密钥，只在本机
go run ./cmd/agents-desk migrate up
go run ./cmd/agents-desk seed                                        # 开发用的数据：平台管理员 admin@example.com、示例工作区 demo（主管理员 owner@、管理员 manager@、坐席 agent@example.com，团队“一线客服”），密码都是 password
bun run dev                                                          # 管理员端在 http://localhost:8081/admin/
```

## 常用命令

```bash
bun run dev                       # 运行应用和前端的开发服务器，改代码后自动重新编译；打开 http://localhost:8081
bun run generate                  # 接口变了以后重新生成前端的客户端（dev 会自动运行）
bun run build                     # 构建可执行文件 bin/agents-desk，前端打包在其中
bun run typecheck                 # 前端的类型检查
bun run agents                    # 升级框架之后，更新框架文档、技能和 AI 编程助手的 MCP 配置
go run ./cmd/agents-desk migrate up   # 执行迁移；migrate make <名称> 新建迁移，migrate publish 发布框架的迁移
go run ./cmd/agents-desk seed       # 写入开发和演示用的账号和工作区，可以重复运行；只在开发和演示环境中运行
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
locales/                 翻译文件：错误提示、资源和字段、枚举和权限的显示文字
docs/                    产品设计和开发计划
config/app.yaml          配置；本机的覆盖写在 config/local.yaml，不提交，命令和 bun run dev 自动叠加它
apps/<前端>/             TanStack Start 前端，挂载路径写在 package.json 的 tenon 字段
packages/client/         bun run generate 生成的客户端，不手改
.tenon/                  tenon 复制的框架前端包和文档，不提交
```

## 提交前

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
bun run typecheck
bun run generate && git diff --exit-code   # 改了接口要提交重新生成的客户端
```

CI（`.github/workflows/ci.yml`）在每次推送和 PR 中运行同样的检查，并构建一次；生成的代码（客户端、路由树、技能）没有提交时失败。

## 编写测试

- 顶层测试的第一行调用 `t.Parallel()`。修改全局状态的测试串行运行，例如用到 `t.Setenv`、`slog.SetDefault` 的测试。
- 依赖数据库的测试用 `internal/apptest` 的 `New`（其中是 `database/dbtest` 的 `Open`）为每个测试建一个临时库，需要环境变量 `POSTGRES_TEST_URL`。本机用 `docker compose -f compose.test.yml up -d --wait` 启动测试用的 PostgreSQL，然后 `export POSTGRES_TEST_URL='postgres://postgres:postgres@127.0.0.1:15433/postgres?sslmode=disable'`。
- 能在进程内模拟的外部服务就在进程内模拟：S3 用 [gofakes3](https://github.com/johannesboyne/gofakes3)，HTTP API 用 `httptest`。
- 与时间有关的逻辑用 `testing/synctest` 控制时间。SQL 中的时间由 Go 算出后作为参数传入，不用 `NOW()` 这类数据库的时钟函数，这样假时钟对查询同样有效。

## 依赖的文档

写到依赖的地方时，先查它们给 AI 的文档。版本以 `go.mod` 和 `apps/web/package.json` 为准；网上的文档是最新的版本，两者有出入时以项目中的版本为准。

- Go 的依赖：MCP 服务 `gopls`（查找符号、读包的 API、检查编译错误），或者 `go doc <包>.<名称>`。
- Mantine：MCP 服务 `mantine`（组件的属性和用法）、技能 `mantine-form`、`mantine-combobox`、`mantine-custom-components`，文档目录 <https://mantine.dev/llms.txt>。
- TanStack：Router 随包发布的技能经 TanStack Intent 加载，见本文开头 `intent-skills` 一段，允许的包写在 `package.json` 的 `intent.skills` 中；文档目录 <https://tanstack.com/router/latest/llms.txt>、<https://tanstack.com/start/latest/llms.txt>、<https://tanstack.com/query/latest/llms.txt>。
- React：<https://react.dev/llms.txt>；Vite：<https://vite.dev/llms.txt>；Bun：<https://bun.com/llms.txt>。

依赖的写法与本文、`.tenon/docs` 冲突时，以本文和 `.tenon/docs` 为准：例如业务逻辑写在 Go 的接口中，前端只负责展示和交互。

## 代码约定

### 业务动作

- 一个用例写成一个业务动作，放在 `internal/<模块>/` 中，一个动作一个文件。HTTP 接口、命令行、队列任务、MCP 工具都是调用它的入口，入口只把外部的输入转换成动作的参数。
- 业务规则、权限判断和校验写在 Go 中；前端和 App 只负责展示和交互。
- 业务错误用 `errorx.Define` 定义稳定的错误码，客户端按错误码处理；启用本地化后，给用户看的提示写在每种语言的翻译文件中，以错误码为消息 ID。

### 工作区和账号

- 工作区在代码中统一叫 tenant，与 Tenon 的租户一致：表 `tenants`、列 `tenant_id`、包 `internal/tenant`、权限 `tenants:*`。给用户看的文字叫“工作区”。
- 工作区的 ID 是 UUIDv7，作为租户、authz 的域和业务表的 `tenant_id`，创建后保持不变；地址用 slug（`/t/{slug}/`），只在租户中间件中由 slug 换成 ID。工作区的链接由 `tenant.Addresses` 生成。
- 属于工作区的表都有 `tenant_id`，唯一约束和常用索引以它开头；每个查询都带上 `tenant_id = sub.Tenant`，每个业务动作都有“其他工作区访问不到”的测试。
- 平台管理员和工作区成员是两套账号：各自一张表、一个 `identity.SubjectType`、一个 `login.Realm`，会话 cookie 分别是 `__Host-admin` 和 `__Host-session`。成员账号是全局的，在每个工作区中的身份由角色决定。

### 数据与事务

- 几条写入要一起成功时放在一个 `database.RunInTx` 中；队列任务与业务数据在同一个事务中发布。
- 回滚时撤销不了的操作（删除缓存、广播）用 `tx.AfterCommit` 登记，提交之后才执行。
- 缓存中的数据不带权限：命中缓存之后仍要检查访问权限。

### 注释

- 包、类型、函数（包括非导出的函数和实现接口的方法）都写注释，直接说明它是什么、做什么、返回什么。
- 结构体字段的含义写在字段行尾的注释中。
- 较长的函数可以按步骤加几行注释，说明每一段做什么。
- 注释用简洁的直述句，直接说明它是什么，不用否定或对比来描述。

### 函数

- 保持函数的完整阅读流，单个函数可以写到 100–200 行。只在代码被复用、或者一段职责明显独立时才抽出函数。
- 标准库和依赖直接调用，不再包一层；直接使用社区组件的原生类型（`*bun.DB`、`*slog.Logger`、`*cobra.Command` 等）。
- 所有依赖都通过参数显式传递，在入口中创建；包级变量只用于只读的值（错误码、正则表达式等）。
- 存储接收 `bun.IDB`，可以用连接池或业务事务构造；自己开启事务的服务接收 `*bun.DB`。

### 错误

尽早暴露失败和错误：出错时返回错误，调用方处理不了就继续向上返回。不为不会发生的情况加防御检查，不吞掉错误，也不用默认值掩盖错误。

### 前端

- 界面用 Mantine 和 `@go-tenon/ui` 的组件，调用接口用生成的客户端和 TanStack Query，路由用 TanStack Router 的文件路由。
- 颜色从 Mantine 的主题中取：品牌色是 `brand`，组件不写 `color` 时用的就是它；其他彩色写主题中预设的名称，例如 `color="deep-orange"`、`var(--mantine-color-deep-orange-6)`；中性色用 `gray`、`dark` 色板、`c="dimmed"` 和 `--tn-*` 变量。不写十六进制或 `rgb()` 的值，写死的值不随品牌色和暗色模式变化。
- 状态的颜色固定：成功用 `green`，进行中用 `bright-blue`，等待处理用 `yellow`，需要注意用 `deep-orange`，失败用 `red`，没有状态用 `gray`。
- 生成的代码（`packages/client`、`routeTree.gen.ts`）不手改，下次生成时会被覆盖。

### 接口

- 改了接口后运行 `bun run generate`，把生成的客户端和接口一起提交。
- 每个接口都能用 `Authorization: Bearer` 调用：浏览器中的网页用会话 cookie 携带 token，App、桌面端和其他服务用 Bearer token，服务端校验的是同一个 token。
- 前端需要的元数据（校验规则、权限、枚举）写进 Go 的接口定义，由 OpenAPI 文档带给生成的客户端，不在前端另写一份。
