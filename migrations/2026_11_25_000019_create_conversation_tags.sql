-- +goose Up
CREATE TABLE conversation_tags (
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    tag_id          uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, tag_id)
);

CREATE INDEX conversation_tags_tenant_tag ON conversation_tags (tenant_id, tag_id, conversation_id);

COMMENT ON TABLE conversation_tags IS '会话与标签的多对多关联';
COMMENT ON COLUMN conversation_tags.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_tags.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_tags.tag_id IS '标签 ID';
COMMENT ON COLUMN conversation_tags.created_at IS '打标时间';

-- +goose Down
DROP TABLE conversation_tags;
