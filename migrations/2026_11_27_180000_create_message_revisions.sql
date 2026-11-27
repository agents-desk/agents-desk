-- +goose Up
-- 消息的编辑历史：时间线项保持不可变，编辑作为新的修订追加，读取时投影出最新一版。
-- 这样审计、搜索重建和推送序号都不需要理解“被改过的消息”，原文随时可以还原。

CREATE TABLE message_revisions (
    item_id         uuid NOT NULL REFERENCES timeline_items (id) ON DELETE CASCADE,
    revision        integer NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL,
    text            text NOT NULL,
    editor_id       uuid REFERENCES members (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (item_id, revision)
);

CREATE INDEX message_revisions_tenant_created ON message_revisions (tenant_id, created_at DESC);

COMMENT ON TABLE message_revisions IS '消息的编辑历史：时间线项不可变，编辑作为新的修订追加';
COMMENT ON COLUMN message_revisions.item_id IS '被编辑的时间线项 ID';
COMMENT ON COLUMN message_revisions.revision IS '第几版修订，从 1 递增；时间线项本身是最初的一版';
COMMENT ON COLUMN message_revisions.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN message_revisions.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN message_revisions.text IS '这一版的正文';
COMMENT ON COLUMN message_revisions.editor_id IS '编辑的成员 ID；成员被删除后为空';
COMMENT ON COLUMN message_revisions.created_at IS '编辑时间';

-- +goose Down
DROP TABLE message_revisions;
