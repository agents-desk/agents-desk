-- +goose Up
-- 为已有业务表和字段补齐 COMMENT，满足 dbtest.CheckSchema / dbtest.CheckComments 约定。

COMMENT ON COLUMN platform_admins.id IS '主键';
COMMENT ON COLUMN platform_admins.name IS '显示名称';
COMMENT ON COLUMN platform_admins.created_at IS '创建时间';
COMMENT ON COLUMN platform_admins.updated_at IS '更新时间';
COMMENT ON COLUMN platform_admins.avatar_file_id IS '头像文件 ID，关联 files.id；为空表示未设置头像';

COMMENT ON COLUMN members.id IS '主键';
COMMENT ON COLUMN members.name IS '显示名称';
COMMENT ON COLUMN members.created_at IS '创建时间';
COMMENT ON COLUMN members.updated_at IS '更新时间';
COMMENT ON COLUMN members.avatar_file_id IS '头像文件 ID，关联 files.id；为空表示未设置头像';
COMMENT ON COLUMN members.language IS '界面与译文偏好语言（BCP 47 标签，如 zh-CN、en）';
COMMENT ON COLUMN members.time_zone IS 'IANA 时区名称；为空表示跟随浏览器时区';

COMMENT ON COLUMN tenants.id IS '主键，同时作为 authz 域和业务表的 tenant_id';
COMMENT ON COLUMN tenants.name IS '工作区名称';
COMMENT ON COLUMN tenants.created_at IS '创建时间';
COMMENT ON COLUMN tenants.updated_at IS '更新时间';

COMMENT ON TABLE teams IS '团队收件箱：工作区中一组成员共同处理服务型会话的队列';
COMMENT ON COLUMN teams.id IS '主键';
COMMENT ON COLUMN teams.created_at IS '创建时间';
COMMENT ON COLUMN teams.updated_at IS '更新时间';
COMMENT ON COLUMN teams.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN teams.name IS '团队收件箱名称，同一工作区内唯一';
COMMENT ON COLUMN teams.description IS '团队收件箱说明';
COMMENT ON COLUMN teams.assignment IS '分配方式：manual（手动领取）或 round_robin（轮流分配）';
COMMENT ON COLUMN teams.business_hours IS '单独设置的时区与每周工作时间（hours.Schedule JSON）；为空表示使用工作区工作时间';

COMMENT ON TABLE team_members IS '团队收件箱与成员的多对多关联';
COMMENT ON COLUMN team_members.team_id IS '团队收件箱 ID';
COMMENT ON COLUMN team_members.member_id IS '成员 ID';
COMMENT ON COLUMN team_members.created_at IS '加入时间';
COMMENT ON COLUMN team_members.updated_at IS '更新时间';
COMMENT ON COLUMN team_members.last_assigned_at IS '轮流分配时最近一次在该团队分到会话的时间；为空表示尚未分到过';

COMMENT ON TABLE member_statuses IS '成员在各工作区中的在线状态与离开重分配偏好';
COMMENT ON COLUMN member_statuses.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN member_statuses.member_id IS '成员 ID';
COMMENT ON COLUMN member_statuses.status IS '在线状态：online（在线）或 away（离开）';
COMMENT ON COLUMN member_statuses.reassign IS '离开时是否将名下会话回到团队收件箱重新分配';
COMMENT ON COLUMN member_statuses.updated_at IS '状态更新时间';

COMMENT ON TABLE contacts IS '联系人：与工作区沟通的外部客户或访客线索';
COMMENT ON COLUMN contacts.id IS '主键';
COMMENT ON COLUMN contacts.created_at IS '创建时间';
COMMENT ON COLUMN contacts.updated_at IS '更新时间';
COMMENT ON COLUMN contacts.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN contacts.kind IS '联系人类型：user（已识别用户）或 lead（访客线索）';
COMMENT ON COLUMN contacts.name IS '联系人姓名或昵称';
COMMENT ON COLUMN contacts.email IS '联系人邮箱';
COMMENT ON COLUMN contacts.external_user_id IS '业务产品传入的外部用户编号，同一工作区内非空时唯一';
COMMENT ON COLUMN contacts.language IS '联系人的沟通语言（BCP 47 标签）；为空表示尚未识别';
COMMENT ON COLUMN contacts.language_manual IS '语言是否由成员手动指定；为 true 时来消息不再自动覆盖';
COMMENT ON COLUMN contacts.attributes IS '联系人自定义属性值（JSON 对象）';

COMMENT ON TABLE channels IS '接入渠道：工作区收发外部消息的具体入口（如网页聊天插件、测试渠道）';
COMMENT ON COLUMN channels.id IS '主键';
COMMENT ON COLUMN channels.created_at IS '创建时间';
COMMENT ON COLUMN channels.updated_at IS '更新时间';
COMMENT ON COLUMN channels.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN channels.kind IS '渠道类型，决定由哪个连接器收发消息';
COMMENT ON COLUMN channels.name IS '渠道名称';
COMMENT ON COLUMN channels.settings IS '渠道连接器配置（JSON 对象）';
COMMENT ON COLUMN channels.team_id IS '新会话默认进入的团队收件箱 ID；为空表示进入未指派';
COMMENT ON COLUMN channels.enabled IS '是否启用该渠道';

COMMENT ON TABLE contact_identities IS '联系人在各接入渠道中的外部身份映射';
COMMENT ON COLUMN contact_identities.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN contact_identities.channel_id IS '渠道 ID';
COMMENT ON COLUMN contact_identities.external_id IS '联系人在该渠道中的外部标识';
COMMENT ON COLUMN contact_identities.contact_id IS '关联的联系人 ID';
COMMENT ON COLUMN contact_identities.created_at IS '创建时间';

COMMENT ON TABLE conversations IS '会话：工作区与联系人之间的服务型或聊天型对话';
COMMENT ON COLUMN conversations.id IS '主键';
COMMENT ON COLUMN conversations.created_at IS '创建时间';
COMMENT ON COLUMN conversations.updated_at IS '更新时间';
COMMENT ON COLUMN conversations.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversations.kind IS '会话类型：service（服务型）或 chat（聊天型）';
COMMENT ON COLUMN conversations.channel_id IS '来源渠道 ID';
COMMENT ON COLUMN conversations.contact_id IS '关联联系人 ID';
COMMENT ON COLUMN conversations.team_id IS '当前归属团队收件箱 ID；为空表示未指派团队';
COMMENT ON COLUMN conversations.assignee_type IS '负责人类型（如 member）；为空表示未指派负责人';
COMMENT ON COLUMN conversations.assignee_id IS '负责人 ID；为空表示未指派负责人';
COMMENT ON COLUMN conversations.status IS '会话状态：open（进行中）、snoozed（延后）或 closed（已关闭）';
COMMENT ON COLUMN conversations.snoozed_until IS '延后到期时间；仅延后状态下非空';
COMMENT ON COLUMN conversations.priority IS '优先级（如 low、normal、high、urgent）';
COMMENT ON COLUMN conversations.language IS '会话当前识别的语言（BCP 47 标签）';
COMMENT ON COLUMN conversations.waiting_since IS '客户最近一次等待回复的起始时间；成员或 AI 回复后清空';
COMMENT ON COLUMN conversations.first_reply_at IS '首次由成员或 AI 回复的时间';
COMMENT ON COLUMN conversations.closed_at IS '最近一次关闭时间';
COMMENT ON COLUMN conversations.last_message_at IS '最近一条消息的时间';
COMMENT ON COLUMN conversations.last_seq IS '时间线中最新一项的递增序号';
COMMENT ON COLUMN conversations.attributes IS '会话自定义属性值（JSON 对象）';
COMMENT ON COLUMN conversations.rating IS '客户满意度评分（1 至 5 星）；未评价时为空';
COMMENT ON COLUMN conversations.rating_comment IS '客户提交的评价附言';
COMMENT ON COLUMN conversations.rated_at IS '客户最近一次提交评价的时间';
COMMENT ON COLUMN conversations.rating_requested_at IS '最近一次关闭后向客户发出评价邀请的时间';

COMMENT ON TABLE conversation_participants IS '会话参与者记录（联系人、成员等）';
COMMENT ON COLUMN conversation_participants.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_participants.participant_type IS '参与者类型（如 contact、member）';
COMMENT ON COLUMN conversation_participants.participant_id IS '参与者 ID';
COMMENT ON COLUMN conversation_participants.created_at IS '加入会话时间';

COMMENT ON TABLE timeline_items IS '会话时间线：只追加的消息、内部备注与系统事件记录';
COMMENT ON COLUMN timeline_items.id IS '主键';
COMMENT ON COLUMN timeline_items.created_at IS '创建时间';
COMMENT ON COLUMN timeline_items.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN timeline_items.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN timeline_items.seq IS '会话内严格递增的序号';
COMMENT ON COLUMN timeline_items.kind IS '条目类型：message（消息）、note（内部备注）或 event（事件）';
COMMENT ON COLUMN timeline_items.actor_type IS '发起方类型（如 contact、member、system）';
COMMENT ON COLUMN timeline_items.actor_id IS '发起方 ID；系统事件可为空';
COMMENT ON COLUMN timeline_items.visibility IS '可见范围：all（所有人可见）或 members（仅工作区成员可见）';
COMMENT ON COLUMN timeline_items.content IS '条目正文与附件等结构化内容（JSON 对象）';
COMMENT ON COLUMN timeline_items.derived IS '后台生成的派生数据，如各语言译文（JSON 对象）';
COMMENT ON COLUMN timeline_items.channel_id IS '收发该消息对应的渠道 ID';
COMMENT ON COLUMN timeline_items.external_id IS '渠道侧外部消息编号，用于幂等去重';

COMMENT ON TABLE message_deliveries IS '发出消息的渠道送达状态记录';
COMMENT ON COLUMN message_deliveries.item_id IS '关联的时间线条目 ID，主键';
COMMENT ON COLUMN message_deliveries.status IS '送达状态：sending、delivered、read 或 failed';
COMMENT ON COLUMN message_deliveries.external_id IS '消息发出后渠道返回的外部编号';
COMMENT ON COLUMN message_deliveries.error IS '发送失败时的错误说明';
COMMENT ON COLUMN message_deliveries.updated_at IS '状态更新时间';

COMMENT ON TABLE conversation_reads IS '成员在各会话中的已读进度';
COMMENT ON COLUMN conversation_reads.member_id IS '成员 ID';
COMMENT ON COLUMN conversation_reads.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_reads.last_read_seq IS '已读到的最大时间线序号';
COMMENT ON COLUMN conversation_reads.updated_at IS '已读进度更新时间';

COMMENT ON TABLE conversation_mentions IS '内部备注中 @ 提及成员的索引记录';
COMMENT ON COLUMN conversation_mentions.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_mentions.member_id IS '被 @ 的成员 ID';
COMMENT ON COLUMN conversation_mentions.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN conversation_mentions.item_id IS '产生 @ 提及的时间线条目 ID';
COMMENT ON COLUMN conversation_mentions.seq IS '对应时间线条目的序号';
COMMENT ON COLUMN conversation_mentions.created_at IS '创建时间';

COMMENT ON TABLE llm_calls IS '大语言模型调用日志：记录每次请求的输入输出、用量与耗时';
COMMENT ON COLUMN llm_calls.id IS '主键';
COMMENT ON COLUMN llm_calls.created_at IS '调用发起时间';
COMMENT ON COLUMN llm_calls.tenant_id IS '所属工作区 ID；平台级调用可为空';
COMMENT ON COLUMN llm_calls.call_type IS '业务调用类型（如 translation）';
COMMENT ON COLUMN llm_calls.endpoint IS '调用的模型接口地址（不含密钥）';
COMMENT ON COLUMN llm_calls.model IS '实际调用的模型名称';
COMMENT ON COLUMN llm_calls.request IS '发送给模型的请求载荷（JSON 对象）';
COMMENT ON COLUMN llm_calls.response IS '模型返回的文本响应';
COMMENT ON COLUMN llm_calls.prompt_tokens IS '输入 token 数';
COMMENT ON COLUMN llm_calls.completion_tokens IS '输出 token 数（含思考 token）';
COMMENT ON COLUMN llm_calls.reasoning_tokens IS '推理思考消耗的 token 数';
COMMENT ON COLUMN llm_calls.cost IS '供应商返回的调用费用（美元）；未提供时为空';
COMMENT ON COLUMN llm_calls.duration_ms IS '调用耗时（毫秒）';
COMMENT ON COLUMN llm_calls.error IS '调用失败时的错误信息';
COMMENT ON COLUMN llm_calls.target_type IS '关联业务对象类型（如 timeline_item）';
COMMENT ON COLUMN llm_calls.target_id IS '关联业务对象 ID';

COMMENT ON TABLE tags IS '工作区标签：用于标记和筛选会话与联系人';
COMMENT ON COLUMN tags.id IS '主键';
COMMENT ON COLUMN tags.created_at IS '创建时间';
COMMENT ON COLUMN tags.updated_at IS '更新时间';
COMMENT ON COLUMN tags.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN tags.name IS '标签名称，同一工作区内不区分大小写唯一';
COMMENT ON COLUMN tags.color IS '标签展示颜色名（Mantine 主题预设色）';
COMMENT ON COLUMN tags.description IS '标签说明';

COMMENT ON TABLE conversation_tags IS '会话与标签的多对多关联';
COMMENT ON COLUMN conversation_tags.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_tags.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_tags.tag_id IS '标签 ID';
COMMENT ON COLUMN conversation_tags.created_at IS '打标时间';

COMMENT ON TABLE contact_tags IS '联系人与标签的多对多关联';
COMMENT ON COLUMN contact_tags.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN contact_tags.contact_id IS '联系人 ID';
COMMENT ON COLUMN contact_tags.tag_id IS '标签 ID';
COMMENT ON COLUMN contact_tags.created_at IS '打标时间';

COMMENT ON TABLE attribute_definitions IS '自定义属性定义：配置会话或联系人的扩展字段';
COMMENT ON COLUMN attribute_definitions.id IS '主键';
COMMENT ON COLUMN attribute_definitions.created_at IS '创建时间';
COMMENT ON COLUMN attribute_definitions.updated_at IS '更新时间';
COMMENT ON COLUMN attribute_definitions.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN attribute_definitions.target IS '属性所属对象类型（如 conversation、contact）';
COMMENT ON COLUMN attribute_definitions.key IS '属性键名，同一工作区和对象下唯一且创建后不可改';
COMMENT ON COLUMN attribute_definitions.name IS '属性显示名称';
COMMENT ON COLUMN attribute_definitions.description IS '属性说明';
COMMENT ON COLUMN attribute_definitions.type IS '属性数据类型（如 text、number、boolean、date、select、multi_select）';
COMMENT ON COLUMN attribute_definitions.options IS '下拉单选或多选属性的可选项列表（JSON 数组）';
COMMENT ON COLUMN attribute_definitions.position IS '展示排序序号';

COMMENT ON TABLE automation_rules IS '自动化规则：定义触发器、条件和动作';
COMMENT ON COLUMN automation_rules.id IS '主键';
COMMENT ON COLUMN automation_rules.created_at IS '创建时间';
COMMENT ON COLUMN automation_rules.updated_at IS '更新时间';
COMMENT ON COLUMN automation_rules.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN automation_rules.builtin_key IS '内置规则标识；自定义规则为空';
COMMENT ON COLUMN automation_rules.name IS '规则名称';
COMMENT ON COLUMN automation_rules.trigger IS '触发事件类型';
COMMENT ON COLUMN automation_rules.conditions IS '规则匹配条件定义（JSON 对象）';
COMMENT ON COLUMN automation_rules.actions IS '匹配后执行的动作列表（JSON 数组）';
COMMENT ON COLUMN automation_rules.enabled IS '规则是否启用';
COMMENT ON COLUMN automation_rules.position IS '同一触发器下的执行排序序号';

COMMENT ON TABLE automation_runs IS '自动化规则执行记录与幂等去重日志';
COMMENT ON COLUMN automation_runs.id IS '主键';
COMMENT ON COLUMN automation_runs.created_at IS '执行时间';
COMMENT ON COLUMN automation_runs.updated_at IS '更新时间';
COMMENT ON COLUMN automation_runs.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN automation_runs.rule_id IS '执行的自动化规则 ID';
COMMENT ON COLUMN automation_runs.conversation_id IS '目标会话 ID';
COMMENT ON COLUMN automation_runs.trigger_item_id IS '触发本次执行的时间线条目 ID';
COMMENT ON COLUMN automation_runs.dedup_key IS '幂等去重键，同一规则下唯一';
COMMENT ON COLUMN automation_runs.status IS '执行状态（如 succeeded、failed、skipped）';
COMMENT ON COLUMN automation_runs.error IS '执行失败时的错误信息';

-- +goose Down
COMMENT ON COLUMN automation_runs.error IS NULL;
COMMENT ON COLUMN automation_runs.status IS NULL;
COMMENT ON COLUMN automation_runs.dedup_key IS NULL;
COMMENT ON COLUMN automation_runs.trigger_item_id IS NULL;
COMMENT ON COLUMN automation_runs.conversation_id IS NULL;
COMMENT ON COLUMN automation_runs.rule_id IS NULL;
COMMENT ON COLUMN automation_runs.tenant_id IS NULL;
COMMENT ON COLUMN automation_runs.updated_at IS NULL;
COMMENT ON COLUMN automation_runs.created_at IS NULL;
COMMENT ON COLUMN automation_runs.id IS NULL;
COMMENT ON TABLE automation_runs IS NULL;

COMMENT ON COLUMN automation_rules.position IS NULL;
COMMENT ON COLUMN automation_rules.enabled IS NULL;
COMMENT ON COLUMN automation_rules.actions IS NULL;
COMMENT ON COLUMN automation_rules.conditions IS NULL;
COMMENT ON COLUMN automation_rules.trigger IS NULL;
COMMENT ON COLUMN automation_rules.name IS NULL;
COMMENT ON COLUMN automation_rules.builtin_key IS NULL;
COMMENT ON COLUMN automation_rules.tenant_id IS NULL;
COMMENT ON COLUMN automation_rules.updated_at IS NULL;
COMMENT ON COLUMN automation_rules.created_at IS NULL;
COMMENT ON COLUMN automation_rules.id IS NULL;
COMMENT ON TABLE automation_rules IS NULL;

COMMENT ON COLUMN attribute_definitions.position IS NULL;
COMMENT ON COLUMN attribute_definitions.options IS NULL;
COMMENT ON COLUMN attribute_definitions.type IS NULL;
COMMENT ON COLUMN attribute_definitions.description IS NULL;
COMMENT ON COLUMN attribute_definitions.name IS NULL;
COMMENT ON COLUMN attribute_definitions.key IS NULL;
COMMENT ON COLUMN attribute_definitions.target IS NULL;
COMMENT ON COLUMN attribute_definitions.tenant_id IS NULL;
COMMENT ON COLUMN attribute_definitions.updated_at IS NULL;
COMMENT ON COLUMN attribute_definitions.created_at IS NULL;
COMMENT ON COLUMN attribute_definitions.id IS NULL;
COMMENT ON TABLE attribute_definitions IS NULL;

COMMENT ON COLUMN contact_tags.created_at IS NULL;
COMMENT ON COLUMN contact_tags.tag_id IS NULL;
COMMENT ON COLUMN contact_tags.contact_id IS NULL;
COMMENT ON COLUMN contact_tags.tenant_id IS NULL;
COMMENT ON TABLE contact_tags IS NULL;

COMMENT ON COLUMN conversation_tags.created_at IS NULL;
COMMENT ON COLUMN conversation_tags.tag_id IS NULL;
COMMENT ON COLUMN conversation_tags.conversation_id IS NULL;
COMMENT ON COLUMN conversation_tags.tenant_id IS NULL;
COMMENT ON TABLE conversation_tags IS NULL;

COMMENT ON COLUMN tags.description IS NULL;
COMMENT ON COLUMN tags.color IS NULL;
COMMENT ON COLUMN tags.name IS NULL;
COMMENT ON COLUMN tags.tenant_id IS NULL;
COMMENT ON COLUMN tags.updated_at IS NULL;
COMMENT ON COLUMN tags.created_at IS NULL;
COMMENT ON COLUMN tags.id IS NULL;
COMMENT ON TABLE tags IS NULL;

COMMENT ON COLUMN llm_calls.target_id IS NULL;
COMMENT ON COLUMN llm_calls.target_type IS NULL;
COMMENT ON COLUMN llm_calls.error IS NULL;
COMMENT ON COLUMN llm_calls.duration_ms IS NULL;
COMMENT ON COLUMN llm_calls.cost IS NULL;
COMMENT ON COLUMN llm_calls.reasoning_tokens IS NULL;
COMMENT ON COLUMN llm_calls.completion_tokens IS NULL;
COMMENT ON COLUMN llm_calls.prompt_tokens IS NULL;
COMMENT ON COLUMN llm_calls.response IS NULL;
COMMENT ON COLUMN llm_calls.request IS NULL;
COMMENT ON COLUMN llm_calls.model IS NULL;
COMMENT ON COLUMN llm_calls.endpoint IS NULL;
COMMENT ON COLUMN llm_calls.call_type IS NULL;
COMMENT ON COLUMN llm_calls.tenant_id IS NULL;
COMMENT ON COLUMN llm_calls.created_at IS NULL;
COMMENT ON COLUMN llm_calls.id IS NULL;
COMMENT ON TABLE llm_calls IS NULL;

COMMENT ON COLUMN conversation_mentions.created_at IS NULL;
COMMENT ON COLUMN conversation_mentions.seq IS NULL;
COMMENT ON COLUMN conversation_mentions.item_id IS NULL;
COMMENT ON COLUMN conversation_mentions.conversation_id IS NULL;
COMMENT ON COLUMN conversation_mentions.member_id IS NULL;
COMMENT ON COLUMN conversation_mentions.tenant_id IS NULL;
COMMENT ON TABLE conversation_mentions IS NULL;

COMMENT ON COLUMN conversation_reads.updated_at IS NULL;
COMMENT ON COLUMN conversation_reads.last_read_seq IS NULL;
COMMENT ON COLUMN conversation_reads.conversation_id IS NULL;
COMMENT ON COLUMN conversation_reads.member_id IS NULL;
COMMENT ON TABLE conversation_reads IS NULL;

COMMENT ON COLUMN message_deliveries.updated_at IS NULL;
COMMENT ON COLUMN message_deliveries.error IS NULL;
COMMENT ON COLUMN message_deliveries.external_id IS NULL;
COMMENT ON COLUMN message_deliveries.status IS NULL;
COMMENT ON COLUMN message_deliveries.item_id IS NULL;
COMMENT ON TABLE message_deliveries IS NULL;

COMMENT ON COLUMN timeline_items.external_id IS NULL;
COMMENT ON COLUMN timeline_items.channel_id IS NULL;
COMMENT ON COLUMN timeline_items.derived IS NULL;
COMMENT ON COLUMN timeline_items.content IS NULL;
COMMENT ON COLUMN timeline_items.visibility IS NULL;
COMMENT ON COLUMN timeline_items.actor_id IS NULL;
COMMENT ON COLUMN timeline_items.actor_type IS NULL;
COMMENT ON COLUMN timeline_items.kind IS NULL;
COMMENT ON COLUMN timeline_items.seq IS NULL;
COMMENT ON COLUMN timeline_items.conversation_id IS NULL;
COMMENT ON COLUMN timeline_items.tenant_id IS NULL;
COMMENT ON COLUMN timeline_items.created_at IS NULL;
COMMENT ON COLUMN timeline_items.id IS NULL;
COMMENT ON TABLE timeline_items IS NULL;

COMMENT ON COLUMN conversation_participants.created_at IS NULL;
COMMENT ON COLUMN conversation_participants.participant_id IS NULL;
COMMENT ON COLUMN conversation_participants.participant_type IS NULL;
COMMENT ON COLUMN conversation_participants.conversation_id IS NULL;
COMMENT ON TABLE conversation_participants IS NULL;

COMMENT ON COLUMN conversations.rating_requested_at IS NULL;
COMMENT ON COLUMN conversations.rated_at IS NULL;
COMMENT ON COLUMN conversations.rating_comment IS NULL;
COMMENT ON COLUMN conversations.rating IS NULL;
COMMENT ON COLUMN conversations.attributes IS NULL;
COMMENT ON COLUMN conversations.last_seq IS NULL;
COMMENT ON COLUMN conversations.last_message_at IS NULL;
COMMENT ON COLUMN conversations.closed_at IS NULL;
COMMENT ON COLUMN conversations.first_reply_at IS NULL;
COMMENT ON COLUMN conversations.waiting_since IS NULL;
COMMENT ON COLUMN conversations.language IS NULL;
COMMENT ON COLUMN conversations.priority IS NULL;
COMMENT ON COLUMN conversations.snoozed_until IS NULL;
COMMENT ON COLUMN conversations.status IS NULL;
COMMENT ON COLUMN conversations.assignee_id IS NULL;
COMMENT ON COLUMN conversations.assignee_type IS NULL;
COMMENT ON COLUMN conversations.team_id IS NULL;
COMMENT ON COLUMN conversations.contact_id IS NULL;
COMMENT ON COLUMN conversations.channel_id IS NULL;
COMMENT ON COLUMN conversations.kind IS NULL;
COMMENT ON COLUMN conversations.tenant_id IS NULL;
COMMENT ON COLUMN conversations.updated_at IS NULL;
COMMENT ON COLUMN conversations.created_at IS NULL;
COMMENT ON COLUMN conversations.id IS NULL;
COMMENT ON TABLE conversations IS NULL;

COMMENT ON COLUMN contact_identities.created_at IS NULL;
COMMENT ON COLUMN contact_identities.contact_id IS NULL;
COMMENT ON COLUMN contact_identities.external_id IS NULL;
COMMENT ON COLUMN contact_identities.channel_id IS NULL;
COMMENT ON COLUMN contact_identities.tenant_id IS NULL;
COMMENT ON TABLE contact_identities IS NULL;

COMMENT ON COLUMN channels.enabled IS NULL;
COMMENT ON COLUMN channels.team_id IS NULL;
COMMENT ON COLUMN channels.settings IS NULL;
COMMENT ON COLUMN channels.name IS NULL;
COMMENT ON COLUMN channels.kind IS NULL;
COMMENT ON COLUMN channels.tenant_id IS NULL;
COMMENT ON COLUMN channels.updated_at IS NULL;
COMMENT ON COLUMN channels.created_at IS NULL;
COMMENT ON COLUMN channels.id IS NULL;
COMMENT ON TABLE channels IS NULL;

COMMENT ON COLUMN contacts.attributes IS NULL;
COMMENT ON COLUMN contacts.language_manual IS NULL;
COMMENT ON COLUMN contacts.language IS NULL;
COMMENT ON COLUMN contacts.external_user_id IS NULL;
COMMENT ON COLUMN contacts.email IS NULL;
COMMENT ON COLUMN contacts.name IS NULL;
COMMENT ON COLUMN contacts.kind IS NULL;
COMMENT ON COLUMN contacts.tenant_id IS NULL;
COMMENT ON COLUMN contacts.updated_at IS NULL;
COMMENT ON COLUMN contacts.created_at IS NULL;
COMMENT ON COLUMN contacts.id IS NULL;
COMMENT ON TABLE contacts IS NULL;

COMMENT ON COLUMN member_statuses.updated_at IS NULL;
COMMENT ON COLUMN member_statuses.reassign IS NULL;
COMMENT ON COLUMN member_statuses.status IS NULL;
COMMENT ON COLUMN member_statuses.member_id IS NULL;
COMMENT ON COLUMN member_statuses.tenant_id IS NULL;
COMMENT ON TABLE member_statuses IS NULL;

COMMENT ON COLUMN team_members.last_assigned_at IS NULL;
COMMENT ON COLUMN team_members.updated_at IS NULL;
COMMENT ON COLUMN team_members.created_at IS NULL;
COMMENT ON COLUMN team_members.member_id IS NULL;
COMMENT ON COLUMN team_members.team_id IS NULL;
COMMENT ON TABLE team_members IS NULL;

COMMENT ON COLUMN teams.business_hours IS NULL;
COMMENT ON COLUMN teams.assignment IS NULL;
COMMENT ON COLUMN teams.description IS NULL;
COMMENT ON COLUMN teams.name IS NULL;
COMMENT ON COLUMN teams.tenant_id IS NULL;
COMMENT ON COLUMN teams.updated_at IS NULL;
COMMENT ON COLUMN teams.created_at IS NULL;
COMMENT ON COLUMN teams.id IS NULL;
COMMENT ON TABLE teams IS NULL;

COMMENT ON COLUMN tenants.updated_at IS NULL;
COMMENT ON COLUMN tenants.created_at IS NULL;
COMMENT ON COLUMN tenants.name IS NULL;
COMMENT ON COLUMN tenants.id IS NULL;

COMMENT ON COLUMN members.time_zone IS NULL;
COMMENT ON COLUMN members.language IS NULL;
COMMENT ON COLUMN members.avatar_file_id IS NULL;
COMMENT ON COLUMN members.updated_at IS NULL;
COMMENT ON COLUMN members.created_at IS NULL;
COMMENT ON COLUMN members.name IS NULL;
COMMENT ON COLUMN members.id IS NULL;

COMMENT ON COLUMN platform_admins.avatar_file_id IS NULL;
COMMENT ON COLUMN platform_admins.updated_at IS NULL;
COMMENT ON COLUMN platform_admins.created_at IS NULL;
COMMENT ON COLUMN platform_admins.name IS NULL;
COMMENT ON COLUMN platform_admins.id IS NULL;
