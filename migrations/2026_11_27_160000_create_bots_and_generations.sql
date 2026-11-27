-- +goose Up
-- AI 参与者的骨架：机器人（bots）与它们的生成（generations）。
-- 生成是可变、短生命周期的状态，模型逐字产出的内容只写在这里；定稿时才在同一个事务里追加一条不可变的时间线消息，
-- 时间线因此保持只追加。

CREATE TABLE bots (
    id         uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    tenant_id  uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    settings   jsonb NOT NULL DEFAULT '{}'
);

CREATE INDEX bots_tenant ON bots (tenant_id, created_at);

COMMENT ON TABLE bots IS '机器人：工作区中的 AI 助手或外部 agent，可以作为参与者加入会话';
COMMENT ON COLUMN bots.id IS '主键';
COMMENT ON COLUMN bots.created_at IS '创建时间';
COMMENT ON COLUMN bots.updated_at IS '更新时间';
COMMENT ON COLUMN bots.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN bots.name IS '机器人的显示名称';
COMMENT ON COLUMN bots.enabled IS '是否启用；停用的机器人不再回复';
COMMENT ON COLUMN bots.settings IS '模型、系统提示词等配置（JSON 对象），内容由具体用途决定';

CREATE TABLE generations (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    actor_id        uuid NOT NULL REFERENCES bots (id) ON DELETE CASCADE,
    trigger_item_id uuid NOT NULL REFERENCES timeline_items (id) ON DELETE CASCADE,
    state           text NOT NULL
        CHECK (state IN ('pending', 'streaming', 'waiting_approval', 'completed', 'failed', 'canceled')),
    parts           jsonb NOT NULL DEFAULT '{}',
    checkpoint_seq  bigint NOT NULL DEFAULT 0,
    item_id         uuid REFERENCES timeline_items (id) ON DELETE SET NULL,
    error           text NOT NULL DEFAULT '',
    usage           jsonb NOT NULL DEFAULT '{}',
    expires_at      timestamptz NOT NULL,
    CHECK (state <> 'completed' OR item_id IS NOT NULL)
);

CREATE UNIQUE INDEX generations_trigger ON generations (conversation_id, trigger_item_id, actor_id);
CREATE INDEX generations_tenant_conversation_active ON generations (tenant_id, conversation_id, created_at)
    WHERE state IN ('pending', 'streaming', 'waiting_approval');
CREATE INDEX generations_expires_active ON generations (expires_at)
    WHERE state IN ('pending', 'streaming', 'waiting_approval');

COMMENT ON TABLE generations IS 'AI 回复的生成：可变、短生命周期，定稿时在同一个事务里追加一条时间线消息';
COMMENT ON COLUMN generations.id IS '主键';
COMMENT ON COLUMN generations.created_at IS '创建时间';
COMMENT ON COLUMN generations.updated_at IS '更新时间';
COMMENT ON COLUMN generations.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN generations.conversation_id IS '生成所在的会话 ID';
COMMENT ON COLUMN generations.actor_id IS '产出回复的机器人 ID';
COMMENT ON COLUMN generations.trigger_item_id IS '触发这次生成的时间线项 ID；同一机器人对同一触发项只生成一次';
COMMENT ON COLUMN generations.state IS '阶段：pending、streaming、waiting_approval、completed、failed 或 canceled';
COMMENT ON COLUMN generations.parts IS '已产出的内容（JSON 对象），例如 text';
COMMENT ON COLUMN generations.checkpoint_seq IS '产出片段的序号，客户端用它续上气泡';
COMMENT ON COLUMN generations.item_id IS '定稿后的时间线项 ID；未定稿时为空';
COMMENT ON COLUMN generations.error IS '失败原因，便于排障';
COMMENT ON COLUMN generations.usage IS '模型用量（JSON 对象）';
COMMENT ON COLUMN generations.expires_at IS '有效期；超过它仍未结束的生成由定时任务标记为失败';

-- +goose Down
DROP TABLE generations;
DROP TABLE bots;
