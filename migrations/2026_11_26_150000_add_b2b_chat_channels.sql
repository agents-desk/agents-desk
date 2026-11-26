-- +goose Up
-- 扩展 channels 与 chats 支持成员个人商务渠道（如网页商务沟通链接、Telegram 个人号）及外部商务聊天（单聊与群聊）。

ALTER TABLE channels
    ADD COLUMN owner_member_id uuid REFERENCES members (id) ON DELETE SET NULL;

CREATE INDEX channels_tenant_owner ON channels (tenant_id, owner_member_id) WHERE owner_member_id IS NOT NULL;

COMMENT ON COLUMN channels.owner_member_id IS '个人商务渠道所属的工作区成员 ID；为空表示工作区公共客服渠道';

ALTER TABLE chats
    ADD COLUMN scope            text NOT NULL DEFAULT 'internal' CHECK (scope IN ('internal', 'external')),
    ADD COLUMN channel_id       uuid REFERENCES channels (id) ON DELETE SET NULL,
    ADD COLUMN external_chat_id text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX chats_tenant_channel_external
    ON chats (tenant_id, channel_id, external_chat_id)
    WHERE channel_id IS NOT NULL AND external_chat_id <> '';
CREATE INDEX chats_tenant_scope ON chats (tenant_id, scope);
CREATE INDEX chats_tenant_channel ON chats (tenant_id, channel_id) WHERE channel_id IS NOT NULL;

COMMENT ON COLUMN chats.scope IS '聊天范围：internal（工作区内部聊天）或 external（经个人商务渠道开展的外部商务沟通）';
COMMENT ON COLUMN chats.channel_id IS '外部商务聊天绑定的个人商务渠道 ID；内部聊天为空';
COMMENT ON COLUMN chats.external_chat_id IS '外部渠道中的会话或群聊唯一标识（如 Telegram peer/group ID 或联系人聊天键）；内部聊天为空字符串';

-- +goose Down
DROP INDEX IF EXISTS chats_tenant_channel;
DROP INDEX IF EXISTS chats_tenant_scope;
DROP INDEX IF EXISTS chats_tenant_channel_external;

ALTER TABLE chats
    DROP COLUMN IF EXISTS external_chat_id,
    DROP COLUMN IF EXISTS channel_id,
    DROP COLUMN IF EXISTS scope;

DROP INDEX IF EXISTS channels_tenant_owner;

ALTER TABLE channels
    DROP COLUMN IF EXISTS owner_member_id;
