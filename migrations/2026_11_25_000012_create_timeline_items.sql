-- +goose Up
CREATE TABLE timeline_items (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
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

COMMENT ON TABLE timeline_items IS '会话时间线：消息、内部备注与系统事件记录';
COMMENT ON COLUMN timeline_items.id IS '主键';
COMMENT ON COLUMN timeline_items.created_at IS '创建时间';
COMMENT ON COLUMN timeline_items.updated_at IS '更新时间';
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

-- +goose Down
DROP TABLE timeline_items;
