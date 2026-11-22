-- +goose Up
CREATE TABLE conversations (
    id                  uuid PRIMARY KEY,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    tenant_id           uuid NOT NULL REFERENCES tenants (id),
    kind                text NOT NULL,
    channel_id          uuid REFERENCES channels (id),
    contact_id          uuid REFERENCES contacts (id),
    team_id             uuid REFERENCES teams (id) ON DELETE RESTRICT,
    assignee_type       text NOT NULL DEFAULT '',
    assignee_id         text NOT NULL DEFAULT '',
    status              text NOT NULL,
    snoozed_until       timestamptz,
    priority            text NOT NULL DEFAULT 'normal',
    language            text NOT NULL DEFAULT '',
    opened_at           timestamptz NOT NULL,
    waiting_since       timestamptz,
    first_reply_at      timestamptz,
    closed_at           timestamptz,
    last_message_at     timestamptz NOT NULL,
    last_seq            bigint NOT NULL DEFAULT 0,
    attributes          jsonb NOT NULL DEFAULT '{}',
    rating              smallint,
    rating_comment      text NOT NULL DEFAULT '',
    rated_at            timestamptz,
    rating_requested_at timestamptz
);
CREATE INDEX conversations_tenant_status ON conversations (tenant_id, status, last_message_at DESC);
CREATE INDEX conversations_contact ON conversations (contact_id, last_message_at DESC);
CREATE INDEX conversations_tenant_assignee ON conversations (tenant_id, assignee_id, status);
CREATE INDEX conversations_tenant_team ON conversations (tenant_id, team_id, status);
CREATE INDEX conversations_tenant_kind_created ON conversations (tenant_id, kind, created_at DESC);
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
COMMENT ON COLUMN conversations.opened_at IS '当前这一轮（新建或重新打开）的开始时间';
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

CREATE TABLE conversation_participants (
    conversation_id  uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    participant_type text NOT NULL,
    participant_id   text NOT NULL,
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, participant_type, participant_id)
);
COMMENT ON TABLE conversation_participants IS '会话参与者记录（联系人、成员等）';
COMMENT ON COLUMN conversation_participants.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_participants.participant_type IS '参与者类型（如 contact、member）';
COMMENT ON COLUMN conversation_participants.participant_id IS '参与者 ID';
COMMENT ON COLUMN conversation_participants.created_at IS '加入会话时间';

CREATE TABLE conversation_rounds (
    id                  uuid PRIMARY KEY,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    tenant_id           uuid NOT NULL REFERENCES tenants (id),
    conversation_id     uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    round               integer NOT NULL,
    team_id             uuid REFERENCES teams (id) ON DELETE RESTRICT,
    assignee_type       text NOT NULL DEFAULT '',
    assignee_id         text NOT NULL DEFAULT '',
    closed_by_type      text NOT NULL DEFAULT '',
    closed_by_id        text NOT NULL DEFAULT '',
    opened_at           timestamptz NOT NULL,
    first_reply_at      timestamptz,
    first_reply_by_type text NOT NULL DEFAULT '',
    first_reply_by_id   text NOT NULL DEFAULT '',
    closed_at           timestamptz,
    rating              smallint,
    rating_comment      text NOT NULL DEFAULT '',
    rated_at            timestamptz,
    UNIQUE (conversation_id, round)
);
CREATE INDEX conversation_rounds_tenant_opened ON conversation_rounds (tenant_id, opened_at DESC);
CREATE INDEX conversation_rounds_tenant_first_reply ON conversation_rounds (tenant_id, first_reply_at DESC) WHERE first_reply_at IS NOT NULL;
CREATE INDEX conversation_rounds_tenant_closed ON conversation_rounds (tenant_id, closed_at DESC) WHERE closed_at IS NOT NULL;
CREATE INDEX conversation_rounds_tenant_rated ON conversation_rounds (tenant_id, rated_at DESC) WHERE rated_at IS NOT NULL;
CREATE INDEX conversation_rounds_tenant_team ON conversation_rounds (tenant_id, team_id, opened_at DESC);
CREATE INDEX conversation_rounds_tenant_assignee ON conversation_rounds (tenant_id, assignee_id, closed_at DESC) WHERE closed_at IS NOT NULL;
CREATE INDEX conversation_rounds_tenant_first_replier ON conversation_rounds (tenant_id, first_reply_by_id, first_reply_at DESC) WHERE first_reply_at IS NOT NULL;
COMMENT ON TABLE conversation_rounds IS '服务型会话的每一轮服务记录，从打开或重新打开开始，记录首次回复、关闭归属快照与客户评价';
COMMENT ON COLUMN conversation_rounds.id IS '服务轮次 ID（UUIDv7）';
COMMENT ON COLUMN conversation_rounds.created_at IS '创建时间';
COMMENT ON COLUMN conversation_rounds.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_rounds.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_rounds.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN conversation_rounds.round IS '会话内第几轮服务，从 1 递增';
COMMENT ON COLUMN conversation_rounds.team_id IS '本轮归属的团队收件箱 ID（关闭后保留快照）';
COMMENT ON COLUMN conversation_rounds.assignee_type IS '本轮负责人类型，为空表示无负责人';
COMMENT ON COLUMN conversation_rounds.assignee_id IS '本轮负责人编号';
COMMENT ON COLUMN conversation_rounds.closed_by_type IS '执行关闭的发起者类型，未关闭时为空';
COMMENT ON COLUMN conversation_rounds.closed_by_id IS '执行关闭的发起者编号，未关闭时为空';
COMMENT ON COLUMN conversation_rounds.opened_at IS '本轮打开或重新打开的时间';
COMMENT ON COLUMN conversation_rounds.first_reply_at IS '本轮首次回复的时间，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.first_reply_by_type IS '本轮首次回复的发起者类型，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.first_reply_by_id IS '本轮首次回复的发起者编号，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.closed_at IS '本轮关闭的时间，进行中时为空';
COMMENT ON COLUMN conversation_rounds.rating IS '客户对本轮服务的评分（1 到 5 星），未评价时为空';
COMMENT ON COLUMN conversation_rounds.rating_comment IS '客户评价附的一句意见';
COMMENT ON COLUMN conversation_rounds.rated_at IS '客户评价的时间';

CREATE TABLE timeline_items (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    seq             bigint NOT NULL,
    kind            text NOT NULL,
    actor_type      text NOT NULL,
    actor_id        text NOT NULL DEFAULT '',
    visibility      text NOT NULL,
    content         jsonb NOT NULL,
    derived         jsonb NOT NULL DEFAULT '{}',
    channel_id      uuid REFERENCES channels (id),
    external_id     text,
    UNIQUE (conversation_id, seq)
);
CREATE UNIQUE INDEX timeline_items_external ON timeline_items (channel_id, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX timeline_items_tenant_message_created ON timeline_items (tenant_id, created_at DESC) WHERE kind = 'message';
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

CREATE TABLE timeline_item_files (
    item_id  uuid NOT NULL REFERENCES timeline_items (id) ON DELETE CASCADE,
    file_id  uuid NOT NULL REFERENCES files (id),
    position int  NOT NULL CHECK (position >= 0),
    PRIMARY KEY (item_id, position),
    CONSTRAINT timeline_item_files_file UNIQUE (file_id)
);
COMMENT ON TABLE timeline_item_files IS '时间线消息和内部备注附带的图片和文件';
COMMENT ON COLUMN timeline_item_files.item_id IS '所属时间线项的 ID';
COMMENT ON COLUMN timeline_item_files.file_id IS '关联的已确认文件 ID（files.id）';
COMMENT ON COLUMN timeline_item_files.position IS '附件在这条消息中的顺序，从 0 开始';

CREATE TABLE message_deliveries (
    item_id     uuid PRIMARY KEY REFERENCES timeline_items (id) ON DELETE CASCADE,
    status      text NOT NULL,
    external_id text NOT NULL DEFAULT '',
    error       text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL
);
COMMENT ON TABLE message_deliveries IS '发出消息的渠道送达状态记录';
COMMENT ON COLUMN message_deliveries.item_id IS '关联的时间线条目 ID，主键';
COMMENT ON COLUMN message_deliveries.status IS '送达状态：sending、delivered、read 或 failed';
COMMENT ON COLUMN message_deliveries.external_id IS '消息发出后渠道返回的外部编号';
COMMENT ON COLUMN message_deliveries.error IS '发送失败时的错误说明';
COMMENT ON COLUMN message_deliveries.updated_at IS '状态更新时间';

CREATE TABLE conversation_reads (
    member_id       uuid NOT NULL REFERENCES members (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    last_read_seq   bigint NOT NULL,
    updated_at      timestamptz NOT NULL,
    PRIMARY KEY (member_id, conversation_id)
);
COMMENT ON TABLE conversation_reads IS '成员在各会话中的已读进度';
COMMENT ON COLUMN conversation_reads.member_id IS '成员 ID';
COMMENT ON COLUMN conversation_reads.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_reads.last_read_seq IS '已读到的最大时间线序号';
COMMENT ON COLUMN conversation_reads.updated_at IS '已读进度更新时间';

CREATE TABLE conversation_mentions (
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    member_id       uuid NOT NULL REFERENCES members (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    item_id         uuid NOT NULL REFERENCES timeline_items (id) ON DELETE CASCADE,
    seq             bigint NOT NULL,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (item_id, member_id)
);
CREATE INDEX conversation_mentions_member ON conversation_mentions (tenant_id, member_id, conversation_id);
COMMENT ON TABLE conversation_mentions IS '内部备注中 @ 提及成员的索引记录';
COMMENT ON COLUMN conversation_mentions.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_mentions.member_id IS '被 @ 的成员 ID';
COMMENT ON COLUMN conversation_mentions.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN conversation_mentions.item_id IS '产生 @ 提及的时间线条目 ID';
COMMENT ON COLUMN conversation_mentions.seq IS '对应时间线条目的序号';
COMMENT ON COLUMN conversation_mentions.created_at IS '创建时间';

-- +goose Down
DROP TABLE conversation_mentions;
DROP TABLE conversation_reads;
DROP TABLE message_deliveries;
DROP TABLE timeline_item_files;
DROP TABLE timeline_items;
DROP TABLE conversation_rounds;
DROP TABLE conversation_participants;
DROP TABLE conversations;
