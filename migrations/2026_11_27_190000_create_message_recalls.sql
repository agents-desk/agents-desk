-- +goose Up
-- 消息的撤回：时间线项保持不可变，撤回作为一条独立的记录追加，读取时投影成“已撤回”。
-- 原文仍留在库里，审计和排障可以还原；对成员和客户只展示撤回后的样子。

CREATE TABLE message_recalls (
    item_id         uuid PRIMARY KEY REFERENCES timeline_items (id) ON DELETE CASCADE,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL,
    recalled_by     uuid REFERENCES members (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL
);

CREATE INDEX message_recalls_tenant_created ON message_recalls (tenant_id, conversation_id, created_at DESC);

COMMENT ON TABLE message_recalls IS '消息的撤回记录：时间线项不可变，撤回作为独立记录追加';
COMMENT ON COLUMN message_recalls.item_id IS '被撤回的时间线项 ID';
COMMENT ON COLUMN message_recalls.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN message_recalls.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN message_recalls.recalled_by IS '撤回的成员 ID；成员被删除后为空';
COMMENT ON COLUMN message_recalls.created_at IS '撤回时间';

-- +goose Down
DROP TABLE message_recalls;
