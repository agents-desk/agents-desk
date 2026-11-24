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

-- +goose Down
DROP TABLE conversations;
