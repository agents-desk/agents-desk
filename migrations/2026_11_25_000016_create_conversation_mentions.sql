-- +goose Up
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
