# Intercom 设计参考：消息、对话、工单与 AI

本文整理 Intercom 的数据模型和 AI（Fin）接入方式，作为 agents-desk 的主要设计参考。luway-cloud 是此前的实验项目，只在最后一节做对照。agents-desk 自己的设计见 [agents-desk 产品设计](product.md)。

资料来自 Intercom REST API 参考（2.18 版）和帮助中心文章（2026 年版本），整理于 2026-11-18。文中两种标记：

- **【文档】**：API 参考或帮助中心明确写了的内容。
- **【推测】**：根据字段和行为推出来的底层设计，Intercom 没有公开说明。

---

## 1. 总体结构

```
身份
  Contact（客户：user 或 lead）
  Admin（客服，也叫 teammate）、Team（团队）
  Bot / Workflow / Fin（AI 和自动化）

线程
  Conversation        一条对话，同时带处理状态、负责人、统计、AI 结果
    └ ConversationPart  时间线：消息、备注、分配、状态变化、属性更新、工作流事件、转交

工单
  Ticket（Customer / Back-office / Tracker）
    ├ TicketType       表单定义：属性和状态
    ├ TicketState      状态：系统类别 + 自定义名字
    ├ TicketPart       工单的时间线
    └ linked_objects   和对话或其他工单的关联
```

一句话概括：**对话是处理问题的单位；时间线记录一切；负责人用团队和个人两个槽位；工单是需要长期跟进时才用的独立对象；AI 是一种负责人，它的结果作为一个结论字段记在对话上。**

---

## 2. Conversation：线程和处理状态在一起

### 2.1 字段【文档】

| 分类 | 字段 | 说明 |
|---|---|---|
| 状态 | `state` | `open`、`closed`、`snoozed`（稍后处理） |
| | `open` | 布尔值，false 表示已关闭 |
| | `snoozed_until` | 稍后处理到什么时候，到时间自动重新打开 |
| | `read` | 是否已读 |
| 负责人 | `team_assignee_id` | 负责团队，0 表示没有 |
| | `admin_assignee_id` | 负责客服，0 表示没有；bot 也会出现在这里 |
| 等待 | `waiting_since` | 客户开始等待的时间；最后一条是客服的回复时为 null |
| 分类 | `priority` | `none`、`low`、`medium`、`high`、`urgent` |
| | `tags`、`custom_attributes` | 标签和自定义属性 |
| SLA | `sla_applied` | 规则名，以及状态 `hit`、`missed`、`canceled`、`active` |
| 来源 | `source` | 开启对话的那条消息：作者、渠道类型、邮件头、附件 |
| | `channel` | 初始渠道和当前渠道，例如 messenger、email、zendesk_ticket |
| 参与者 | `contacts` | 客户，通常只有一个 |
| | `teammates` | 在对话里写过内容的客服 |
| 评价 | `conversation_rating` | 1 到 5 分和评语；被评价的对象可以是 admin、team、bot、workflow |
| 统计 | `statistics` | 见 2.2 |
| 关联 | `linked_objects` | 关联的工单或对话，最多 1000 个 |
| AI | `ai_agent_participated`、`ai_agent` | 见第 5 节 |

### 2.2 统计字段【文档】

- 时长：`time_to_assignment`、`time_to_admin_reply`、`time_to_first_close`、`time_to_last_close`、`median_time_to_reply`、`handling_time`、`adjusted_handling_time`（去掉空闲时间的处理时长）
- 时间点：首次/最后一次分配、首次/最后一次客服回复、首次/最后一次关闭、最后一次客户回复
- 计数：`count_reopens`、`count_assignments`、`count_conversation_parts`
- 按团队计算的首响时间，分自然时间和工作时间两个版本

### 2.3 设计要点

**重开不切周期【文档 + 推测】。** 重开就是状态改回 open，`count_reopens` 加一；统计分“首次关闭”和“最后一次关闭”。Fin 的计费规则印证了“一个对话就是一件事”的前提：每个对话最多计费一次，客户之后回到同一个对话继续求助，之前记的 resolution 会扣回。新问题依靠 Messenger 里的“发新消息”开新对话。

**“在等谁”只用一个时间字段【文档】。** `waiting_since` 在客户发言时记下，客服回复后清空。SLA、收件箱的“等待中”排序都从它算，不需要单独的“在等谁”枚举。

**稍后处理是第三种状态【文档】。** `snoozed` 让客服把暂时处理不了的对话移出视线，到时间自动回来，比“保持开启”或“先关掉”都干净。

---

## 3. ConversationPart：一条时间线记录一切

### 3.1 字段【文档】

| 字段 | 说明 |
|---|---|
| `part_type` | 评论、备注、分配、开启、关闭、稍后处理、属性更新等。完整枚举没有公开，不认识的类型返回 `unknown` |
| `body` | 正文，可以是 HTML |
| `author` | 客户、客服或团队；bot 和活动以客服或团队的名义发送 |
| `author.from_ai_agent` | 这一条由 AI 发出 |
| `author.is_ai_answer` | 正文由 AI 生成 |
| `state` | **这一条产生时**对话的状态 |
| `assigned_to`、`admin_assignee_id`、`team_assignee_id` | 这一条把对话分配给了谁；0 表示取消，没有值表示没变 |
| `receipts` | 投递回执：整体状态加上每个接收者的状态（sending、sent、seen、failed_to_send）和失败原因（退信、被标为垃圾邮件等） |
| `translations` | 同一条消息的多语言版本，`original` 记录原文的语言 |
| `event_details` | 工作流、属性修改、等待结束、身份验证失败，以及 handoff |
| `event_details.handoff` | 转交到外部：类型（`zendesk_ticket`、`zendesk_agent`、`salesforce_case`、`external_agent`、`external_ticket` 等）、是否成功、外部单号 |
| `metadata.quick_reply_options` | 快捷回复选项，以及客户点了哪一个 |
| `attachments`、`redacted`、`external_id`、`app_package_code` | 附件、是否已抹除内容、外部 id、由哪个 API 应用创建 |

### 3.2 推测的底层设计

**时间线是事件流，统计是从它算出来的结果【推测】。**
- 每个 part 都带着产生时的状态和负责人变化。
- `statistics` 里的“首次/最后一次”和计数，正好是把 parts 按顺序重放能算出来的值。

所以 parts 很可能是真实数据，`statistics` 是物化视图。这样 Conversation 本身字段不多；新增报表指标只需要从事件里多算一项，不需要往对话表上加列。

**投递状态挂在消息上【文档 + 推测】。** 回执按接收者记录，一条回复的出站情况（Messenger 已读、邮件退信）都在这条消息上。推测：出站投递是一张以 part 和接收者为键的表，状态异步更新。

---

## 4. Ticket：需要长期跟进时才用的独立工单

### 4.1 三类工单【文档】

| 类型 | 用途 | 客户能否看到 |
|---|---|---|
| Customer | 需要较长时间处理、要向客户展示进度的请求：退款、账号变更、故障排查 | 能（`is_shared`） |
| Back-office | 内部协作：找财务、技术支持、其他部门 | 不能 |
| Tracker | 一个问题影响很多客户（例如线上故障），把相关对话和工单挂在一起，处理完统一通知 | 不能 |

### 4.2 字段【文档】

- `id`（API 使用）和 `ticket_id`（收件箱和 Messenger 里显示的编号）
- `category`、`ticket_type`、`ticket_attributes`（默认有标题和描述）
- `ticket_state`、`previous_ticket_state_id`
- `contacts`、`company_id`、`admin_assignee_id`、`team_assignee_id`、`open`、`snoozed_until`
- `ticket_parts`（时间线，可以记录状态变化和属性修改）、`linked_objects`、`channel`

### 4.3 工单类型就是表单定义【文档】

`TicketType` 有名字、图标、类别、是否仅内部可见、是否归档，以及一组属性。每个属性包括：

- `data_type`：string、list、integer、decimal、boolean、datetime、files，以及 relationship（关联其他对象）
- `required_to_create`：客服建单时必填
- `required_to_create_for_contacts`：客户建单时必填
- `visible_on_create`、`visible_to_contacts`：建单表单里是否显示、客户能否看到
- `input_options`：多行文本、下拉选项

### 4.4 状态分两层【文档】

- `category` 固定四种：`submitted`、`in_progress`、`waiting_on_customer`、`resolved`。导入接口还提到 `on_hold`。
- 每个状态有 `internal_label`（客服看到的名字）和 `external_label`（客户看到的名字），每个工作区可以自定义。

系统逻辑（SLA、报表、自动化）只认四种类别；展示用自定义名字，对内对外还可以用不同的说法。

### 4.5 关联规则【文档】

建工单时可以用 `conversation_to_link_id` 关联已有对话。允许的组合：

- 对话 和 后台工单
- 对话 和 跟踪工单
- 客户工单 和 不对客户公开的后台工单
- 客户工单 和 跟踪工单

对话可以转换成客户工单：`POST /conversations/{id}/convert`，传入工单类型和属性。

### 4.6 客户工单很可能是“升格后的对话”【推测】

依据：

1. 对话可以直接转换成客户工单；Fin 的 Handoff to Ticket 也是“把对话转成工单”。
2. 工单同时有内部 `id` 和显示用的 `ticket_id` 两种编号。
3. 客户工单有自己的回复时间线，客户可以继续在里面回复。

推测：客户工单和对话底层是同一种线程，加上了工单类型、属性和状态；后台工单和跟踪工单才是真正独立的对象，通过关联挂在线程上。文档没有说明转换后 id 是否保持不变。

---

## 5. 接上 AI（Fin）之后

### 5.1 AI 的数据分两层【文档】

**消息层**：每个 part 上有 `from_ai_agent` 和 `is_ai_answer`，记录“这句话是 AI 说的”。

**对话层**：`conversation.ai_agent` 记录“AI 处理的最终结果”：

| 字段 | 取值 |
|---|---|
| `source_type` | Fin 从哪里被启用：`workflow`、`profile`、`workflow_preview`、`fin_preview`、`essentials_plan_setup` |
| `source_title` | 启用它的工作流或配置的名字 |
| `last_answer_type` | `ai_answer`（生成的）、`custom_answer`（预设的） |
| `resolution_state` | `assumed_resolution`（推定解决）、`confirmed_resolution`（确认解决）、`escalated`（升级给人工）、`negative_feedback`（差评）、`procedure_handoff`（流程中交接） |
| `rating`、`rating_remark` | 客户对 AI 的评价 |
| `content_sources` | 回答引用的来源：`article`、`internal_article`、`file`、`external_content`、`content_snippet`、`workflow_connector_action` |

AI 的结论和对话的开关状态是**两个独立维度**：对话可以已关闭但 AI 结论是 escalated，也可以仍开启但 AI 结论是 assumed_resolution。

### 5.2 “解决”的判定和计费【文档】

- AI 给出最后一个回答后，客户明确表示解决了（例如“好的谢谢”），算 **confirmed**。
- 客户没有再要求帮助就离开了，算 **assumed**。
- 只有真正的回答才算，回复一句问候不算。
- AI 在追问时客户不再回复，对话自动关闭，记为 abandoned，**不计费**。
- 客户之后回到同一个对话继续求助，resolution **扣回**。
- 默认升级（客户要人工、情绪激动、对话陷入循环）、工作区升级规则、Procedure 执行失败、垃圾消息：**不计费**。
- Procedure 里配置好的交接（Handoff to team、@handoff）：**计费**。
- 每个对话最多计一次。

### 5.3 编排分三层【文档】

```
Workflow（规则引擎：触发条件、渠道和受众、分配、收集信息、设置属性）
  └─ Fin 步骤
       ├─ 内容问答
       │    RAG 检索帮助中心文章、内部文章、PDF、网页、片段
       │    内容按受众过滤（套餐、地区、品牌）
       ├─ Guidance
       │    语气、篇幅、什么时候澄清问题、什么时候升级给人工
       └─ Procedure（按客户每条消息的意图触发，相当于用自然语言写的 SOP）
            触发条件：“什么时候用这个流程”的描述 + 正反例
            步骤：
              指令（自然语言）
              条件分支（不能嵌套）
              子流程
              调用数据连接器（Shopify、Stripe 等外部 API）
              读取 / 更新对话属性
              等待客户回复（5 分钟到 7 天）
              等待外部 webhook 回调（每次运行一个回调地址）
              查找内容
              拉人工审批后继续（loop in teammate）
            结束方式：
              交给团队或某个人
              交给 workflow（单向，Procedure 不再继续）
              转成客户工单（只能选客户工单类型）
              结束语
            意图变化时可以切换到另一个 Procedure（agentic switch）
```

几个细节【文档】：

- 数据连接器的返回值只是步骤输出，不会自动写进对话属性；要保存需要交给 workflow 去写。
- 每次调用连接器后应加条件判断，否则连接器静默失败可能导致意外升级。
- 工作区级的 Guidance 不会自动用于 Procedure，需要在 Procedure 里选择引用。
- 测试有两种：Preview（客户能看到，不能在线上流程用）和 Simulations（后台跑，可以模拟连接器数据，带通过条件）。
- 高风险内容（自残、涉及未成年人、越狱、医疗/法律/金融的高风险建议）总是交给人工。

### 5.4 升级的优先级【文档】

1. **默认升级**：客户要人工、情绪激动、对话陷入循环。总是生效，Procedure 改不了。
2. **工作区的升级规则和指引**。
3. **Procedure 里配置的交接**。

客户的消息同时匹配升级规则和某个 Procedure 时，升级优先。

### 5.5 转交都是时间线上的事件【文档】

| 转交目标 | 对数据的影响 |
|---|---|
| 交给团队或某个人 | 改负责人槽位，写一条分配 part |
| 交给 workflow | 规则引擎接手，Procedure 结束 |
| 转成工单 | 对话转换成客户工单 |
| 交给外部系统 | 写一条 handoff 事件：类型、是否成功、外部单号 |

### 5.6 AI 可以单独接别家客服系统【文档 + 推测】

Fin Standalone 可以接 Zendesk、Salesforce 等：对话的渠道可以是 `zendesk_ticket`、`zendesk_sunshine`，`external_references` 用来同步外部 id；转交给外部系统时，可以先关闭 Intercom 里的对话，再用数据连接器在外部系统建单。

推测：**在 Intercom 里，Conversation 是 AI 处理问题的单位。** 人工处理可以完全在别的系统里完成，Intercom 只负责 AI 这一层，再把结果和计费记在对话上。

### 5.7 事后分析写回对话【文档 + 推测】

`custom_attributes` 里有系统写入、只读的属性：

- AI Topic and Subtopic：AI 归类的主题和子主题
- CX Score：体验评分、说明和原因

另外还有 `monitor_evaluations`（监控评估）和 `scorecards`（质检评分）。推测：对话关闭后有一批 AI 分析任务，结论写回对话，报表直接按这些属性筛选。

---

## 6. 推测的整体数据流

```
渠道（Messenger、邮件、WhatsApp、电话、Slack、Zendesk…）
  │
  ▼
Conversation 建立（source 记录第一条消息，channel 记录渠道）
  │
  ▼
Workflow 按规则匹配 ──► Fin 接手（bot 占负责人槽位）
  │
  │  客户每发一条消息：
  │    满足升级条件？ ──► 写 handoff / assignment part ──► team 槽位 = 某个团队
  │    意图匹配某个 Procedure？ ──► 按步骤执行（调外部 API、等待、审批）
  │    否则 ──► RAG 回答（part 标记 is_ai_answer，记录引用来源）
  │  ai_agent.resolution_state 随过程更新，一个对话最多计费一次
  │
  ▼
人工处理
  在队列里领取（admin 槽位）
  回复 part（带投递回执）、内部备注 part
  关闭 / 稍后处理 / 重开（count_reopens 加一；客户回来继续求助则扣回 resolution）
  │
  ▼
需要长期跟进
  转成客户工单（类型、表单、四类状态）
  或者关联后台工单（内部协作）、跟踪工单（批量问题）
  │
  ▼
关闭后
  AI 主题归类、CX Score、质检评分 ──► 写回对话属性 ──► 报表
```

---

## 7. 对 agents-desk 的设计要点

按重要程度排列：

1. **对话是处理问题的单位，新问题开新对话。** 不在对话内部切周期；重开只是状态改回 open，计数加一。前提是产品交互要支持“发起新请求”。如果要接企业微信这类始终只有一个聊天窗口的渠道，需要另行设计，见第 8 节。
2. **一条时间线记录一切。** 消息、备注、分配、状态变化、属性修改、转交都是 part，每个 part 记录产生时的状态和负责人。统计从事件算，对话表保持精简。
3. **负责人用“团队 + 个人”两个槽位。** 只有团队就是在队列里；AI 作为一种 bot 负责人，转人工就是改槽位。
4. **“在等谁”用一个 `waiting_since` 字段表示。**
5. **AI 结论是对话上一个独立字段。** 消息层标记哪条是 AI 说的，对话层记录 resolution_state 和引用来源；AI 结论和对话开关状态是两个维度。
6. **状态加入“稍后处理”**，到时间自动回到开启。
7. **工单按需引入。** 起步可以只有对话；需要长期跟进时，按 Intercom 的方式做：工单类型就是表单定义，状态分系统类别和自定义名字两层，客户工单由对话转换而来，后台工单和跟踪工单通过关联挂在对话上。
8. **转交是一等事件**，包括转到外部系统，记录是否成功和外部单号。
9. **AI 编排分层**：规则引擎（workflow）决定谁来接；AI 负责回答和执行 SOP（Procedure）；升级规则的优先级固定，默认升级始终生效。

### 三个场景的补充

agents-desk 是团队自用的产品，包含三个场景：同事聊天、同事之间的服务（IT、人事、行政）、对外客服。Intercom 只覆盖对外客服，所以还要补充：

- **同事聊天**：单聊、群聊没有负责人、队列和 SLA，只用消息层（线程、参与者、消息、已读、@）。聊天里出现要办的事，可以“转为工单”，并关联原消息。
- **同事之间的服务**：请求者也是成员，所以内部备注的可见性要按“在这个对话里是什么角色”判断，不能按“是不是成员”判断；员工身份已知，需要关联组织架构（部门、上级），供路由和审批使用；审批、跨部门流转更常见。
- **对外客服**：基本照 Intercom 的做法，包括渠道接入、客户身份验证、出站投递回执、非工作时间提示。
- **同一个成员在不同场景身份不同**：在群里是参与者，在服务里可能是请求者，在客服里是处理人。身份由所在会话里的角色决定，不由账号决定。

---

## 8. 和 luway-cloud 的对照（次要参考）

| 方面 | Intercom | luway-cloud |
|---|---|---|
| 处理单位 | 对话本身；重开不切周期 | 对话内部切成多个服务周期（`service_sessions`），每次重开新建一个 |
| 负责人 | `team_assignee_id` + `admin_assignee_id` | `assignee_kind`（ai / member / pending_queue）+ `assignee_actor_id` + `team_id` + `home_queue_id`，新旧两套并存 |
| 在等谁 | `waiting_since` 一个字段 | `waiting_on` 枚举 + `awaiting_reply_since`，再由消息用途推出 |
| 统计 | 从时间线事件派生 | 作为列直接写在周期上，周期表四十多列 |
| AI 结果 | 对话上的 `ai_agent.resolution_state` | 周期上的 `ai_only`、`close_reason` 等字段 |
| 正式工单 | 独立的 Ticket（三类） | 没有，交给外部系统（业务连接） |

luway-cloud 需要切周期的原因是接了微信这类始终只有一个聊天窗口的渠道，客户没有“开新对话”的入口。agents-desk 如果也要接这类渠道，可以这样处理：在渠道适配层根据规则（例如上一个对话已关闭超过一定时间）自动开新对话，而不是在对话内部切周期。这样核心模型仍然和 Intercom 一致。

luway-cloud 值得保留的做法：

- 状态只通过一个写入口修改（`servicestate`）。
- 用分配世代号（`assignment_version`）丢弃迟到的 AI 运行结果。

Intercom 文档没有公开这一层的细节，但做 AI 接待都会遇到这个并发问题。

---

## 9. 待验证的问题

- `part_type`、`author.type`、`source.type`、`source.delivered_as` 的完整枚举没有公开。
- 客户工单和对话是否共用同一个 id，转换后 id 是否保持不变。
- Fin 处理期间，对话的 `admin_assignee_id` 具体是什么值（bot 的 id，还是 0 加上某个团队）。
- WhatsApp、邮件这类渠道上，对话关闭后客户再发消息，是重开原对话还是新建对话。
- Copilot（给客服用的 AI 助手）的数据如何记录，本次没有查到资料。

---

## 来源

- [Conversation（API 2.18）](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/conversations/conversation)
- [Conversation Part](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/models/conversation_part)
- [Manage a conversation](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/conversations/manageconversation)
- [Ticket](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/tickets/ticket)
- [Ticket Type](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/tickets/ticket_type)
- [Create a ticket](https://developers.intercom.com/docs/references/rest-api/api.intercom.io/tickets/createticket)
- [Convert a conversation to a ticket（API 2.11）](https://developers.intercom.com/docs/references/2.11/rest-api/api.intercom.io/conversations/convertconversationtoticket)
- [Ticket State（preview）](https://developers.intercom.com/docs/references/preview/rest-api/api.intercom.io/tickets/ticket_state)
- [Fin AI Agent explained](https://www.intercom.com/help/en/articles/7120684-fin-ai-agent-explained)
- [Fin AI Agent outcomes](https://www.intercom.com/help/en/articles/8205718-fin-ai-agent-outcomes)
- [Building Fin Procedures](https://www.intercom.com/help/en/articles/13449439-building-fin-procedures)
- [Hand over Fin AI Agent conversations to another support tool](https://www.intercom.com/help/en/articles/7995955-hand-over-fin-ai-agent-conversations-to-another-support-tool)
