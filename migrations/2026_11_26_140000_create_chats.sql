-- +goose Up
-- 内部聊天扩展表：与 conversations（kind=chat）一对一关联，支持单聊（direct）和群聊（group）。
CREATE TABLE chats (
    conversation_id uuid PRIMARY KEY REFERENCES conversations (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    mode            text NOT NULL CHECK (mode IN ('direct', 'group')),
    name            text NOT NULL DEFAULT '',
    dm_key          text,
    created_by      uuid REFERENCES members (id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX chats_tenant_dm_key ON chats (tenant_id, dm_key) WHERE dm_key IS NOT NULL;
CREATE INDEX chats_tenant_mode ON chats (tenant_id, mode);

COMMENT ON TABLE chats IS '内部聊天扩展表：与 conversations（kind=chat）一对一关联，存储单聊/群聊模式、群名称与单聊幂等键';
COMMENT ON COLUMN chats.conversation_id IS '主键，关联 conversations.id（kind=chat）';
COMMENT ON COLUMN chats.created_at IS '创建时间';
COMMENT ON COLUMN chats.updated_at IS '更新时间';
COMMENT ON COLUMN chats.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN chats.mode IS '聊天模式：direct（单聊）或 group（群聊）';
COMMENT ON COLUMN chats.name IS '群聊名称；单聊为空字符串，由前端展示对方名称';
COMMENT ON COLUMN chats.dm_key IS '单聊幂等唯一键（两个参与者按 type:id 升序拼接，支持 member 与未来 bot）；群聊为空';
COMMENT ON COLUMN chats.created_by IS '创建该聊天的成员 ID；系统创建时为空';

-- +goose Down
DROP TABLE chats;
