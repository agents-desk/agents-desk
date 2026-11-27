-- +goose Up
-- 发给对方的消息不能在原处修改：更正是一条新的消息，指向被更正的原消息。
-- 原消息在读取时投影成“已更正”，客户在自己能看到的渠道里收到更正后的内容。

CREATE TABLE message_corrections (
    original_item_id   uuid PRIMARY KEY REFERENCES timeline_items (id) ON DELETE CASCADE,
    correction_item_id uuid NOT NULL REFERENCES timeline_items (id) ON DELETE CASCADE,
    tenant_id          uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    conversation_id    uuid NOT NULL,
    created_at         timestamptz NOT NULL
);

CREATE UNIQUE INDEX message_corrections_item ON message_corrections (correction_item_id);
CREATE INDEX message_corrections_tenant_created ON message_corrections (tenant_id, conversation_id, created_at DESC);

COMMENT ON TABLE message_corrections IS '消息的更正：更正是一条新消息，指向被更正的原消息；原消息读取时投影成已更正';
COMMENT ON COLUMN message_corrections.original_item_id IS '被更正的时间线项 ID；一条消息只保留最近一次更正';
COMMENT ON COLUMN message_corrections.correction_item_id IS '作为更正发出的时间线项 ID';
COMMENT ON COLUMN message_corrections.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN message_corrections.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN message_corrections.created_at IS '更正时间';

-- +goose Down
DROP TABLE message_corrections;
