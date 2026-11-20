-- +goose Up
-- 收件箱的列表按负责人、团队收件箱和状态查询。
CREATE INDEX conversations_tenant_assignee ON conversations (tenant_id, assignee_id, status);
CREATE INDEX conversations_tenant_team ON conversations (tenant_id, team_id, status);

-- 成员在会话中读到的位置：时间线中序号不大于 last_read_seq 的项已读。只会往前推进。
CREATE TABLE conversation_reads (
    member_id       uuid NOT NULL REFERENCES members (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    last_read_seq   bigint NOT NULL,
    updated_at      timestamptz NOT NULL,
    PRIMARY KEY (member_id, conversation_id)
);

-- 内部备注中 @ 到的成员：一条备注 @ 一位成员是一行。收件箱的“@我”按它列出会话；被 @ 的成员能看到这个会话。
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

-- +goose Down
DROP TABLE conversation_mentions;
DROP TABLE conversation_reads;
DROP INDEX conversations_tenant_team;
DROP INDEX conversations_tenant_assignee;
