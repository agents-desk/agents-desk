-- +goose Up
-- 多协议机器人渠道与内部服务底座：
-- 1. 渠道引入正交服务对象 audience（customer / partner / internal）；
-- 2. 联系人支持 internal（内部联系人）与工作区成员关联 member_id；
-- 3. 新增 external_groups（群内 @ 机器人轻量路由与治理表）与 external_users（群内发言人轻量资料表）；
-- 4. 会话内核泛化支持 external_group_id 与按 (tenant_id, audience, contact_id) / (tenant_id, external_group_id) 唯一活跃约束；
-- 5. 时间线支持 reply_to_item_id 引用回复；
-- 6. 工单关联升级为 ticket_targets（同时支持受影响联系人与外部群）；
-- 7. 商务单聊去掉 contact: 魔法键，改为 conversations 部分唯一索引兜底。

ALTER TABLE channels
    ADD COLUMN audience text NOT NULL DEFAULT 'customer'
        CHECK (audience IN ('customer', 'partner', 'internal'));

UPDATE channels
SET audience = 'partner'
WHERE owner_member_id IS NOT NULL;

CREATE INDEX channels_tenant_audience ON channels (tenant_id, audience);

COMMENT ON COLUMN channels.audience IS '渠道服务对象：customer（外部客户）、partner（合作商家/商务）或 internal（内部员工）';

ALTER TABLE contacts
    ADD COLUMN member_id uuid REFERENCES members (id) ON DELETE SET NULL;

CREATE UNIQUE INDEX contacts_tenant_member
    ON contacts (tenant_id, member_id)
    WHERE member_id IS NOT NULL;

COMMENT ON COLUMN contacts.kind IS '联系人类型：user（已识别客户）、lead（访客线索）或 internal（内部联系人）';
COMMENT ON COLUMN contacts.member_id IS '可选：内部联系人关联的工作区成员 ID，同一工作区内唯一';

CREATE TABLE external_groups (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id      uuid NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    external_id     text NOT NULL,
    name            text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'pending_setup'
        CHECK (status IN ('pending_setup', 'active', 'removed')),
    team_id         uuid REFERENCES teams (id) ON DELETE SET NULL,
    organization_id uuid,
    ai_profile_id   uuid,
    language        text NOT NULL DEFAULT '',
    language_manual boolean NOT NULL DEFAULT false,
    metadata        jsonb NOT NULL DEFAULT '{}',
    last_active_at  timestamptz,
    CONSTRAINT external_groups_channel_external_unique UNIQUE (tenant_id, channel_id, external_id)
);

CREATE INDEX external_groups_tenant_channel ON external_groups (tenant_id, channel_id, status);
CREATE INDEX external_groups_tenant_status ON external_groups (tenant_id, status, updated_at DESC);

COMMENT ON TABLE external_groups IS '外部群路由配置：机器人所在第三方群聊的路由、归属团队、组织与语言配置（不搬运群聊日常消息）';
COMMENT ON COLUMN external_groups.id IS '主键';
COMMENT ON COLUMN external_groups.created_at IS '创建时间';
COMMENT ON COLUMN external_groups.updated_at IS '更新时间';
COMMENT ON COLUMN external_groups.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN external_groups.channel_id IS '所属机器人渠道 ID';
COMMENT ON COLUMN external_groups.external_id IS '第三方平台中的群聊唯一标识（如 Telegram chat_id、企微 chatid）';
COMMENT ON COLUMN external_groups.name IS '外部群名称';
COMMENT ON COLUMN external_groups.status IS '外部群状态：pending_setup（待设置）、active（已启用）或 removed（已移出）';
COMMENT ON COLUMN external_groups.team_id IS '该群请求默认进入的团队收件箱 ID，优先于渠道默认团队';
COMMENT ON COLUMN external_groups.organization_id IS '所属外部组织 ID；为空表示内部群';
COMMENT ON COLUMN external_groups.ai_profile_id IS '该群绑定的 AI 用途 ID；为空表示未启用 AI 接待';
COMMENT ON COLUMN external_groups.language IS '该外部群识别或手动设置的沟通语言（BCP 47）';
COMMENT ON COLUMN external_groups.language_manual IS '群语言是否由成员手动设置';
COMMENT ON COLUMN external_groups.metadata IS '渠道特定的群元数据（JSON 对象）';
COMMENT ON COLUMN external_groups.last_active_at IS '该群最近一次收到机器人请求的时间';

CREATE TABLE external_users (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel_id  uuid NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    external_id text NOT NULL,
    name        text NOT NULL DEFAULT '',
    avatar_url  text NOT NULL DEFAULT '',
    metadata    jsonb NOT NULL DEFAULT '{}',
    CONSTRAINT external_users_channel_external_unique UNIQUE (tenant_id, channel_id, external_id)
);

CREATE INDEX external_users_tenant_channel ON external_users (tenant_id, channel_id);

COMMENT ON TABLE external_users IS '外部群发言人轻量资料：记录群内 @ 机器人或外部群发言人的显示名称与平台账号，不写入客户联系人表';
COMMENT ON COLUMN external_users.id IS '主键';
COMMENT ON COLUMN external_users.created_at IS '创建时间';
COMMENT ON COLUMN external_users.updated_at IS '更新时间';
COMMENT ON COLUMN external_users.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN external_users.channel_id IS '所属渠道 ID';
COMMENT ON COLUMN external_users.external_id IS '发言人在第三方平台中的用户标识（用于展示与回复时 @ 提及）';
COMMENT ON COLUMN external_users.name IS '发言人显示名称';
COMMENT ON COLUMN external_users.avatar_url IS '发言人头像地址';
COMMENT ON COLUMN external_users.metadata IS '渠道特定的发言人元数据（JSON 对象）';

ALTER TABLE conversations
    ADD COLUMN audience text NOT NULL DEFAULT 'customer'
        CHECK (audience IN ('customer', 'partner', 'internal')),
    ADD COLUMN external_group_id uuid REFERENCES external_groups (id) ON DELETE RESTRICT;

UPDATE conversations c
SET audience = ch.audience
FROM channels ch
WHERE c.channel_id = ch.id;

ALTER TABLE conversations
    ADD CONSTRAINT conversations_service_peer_xor CHECK (
        kind <> 'service'
        OR ((contact_id IS NOT NULL) <> (external_group_id IS NOT NULL))
    );

DROP INDEX IF EXISTS conversations_one_active_service_per_contact;

CREATE UNIQUE INDEX conversations_one_active_service_per_contact
    ON conversations (tenant_id, audience, contact_id)
    WHERE kind = 'service' AND contact_id IS NOT NULL AND status IN ('open', 'snoozed');

CREATE UNIQUE INDEX conversations_one_active_service_per_group
    ON conversations (tenant_id, external_group_id)
    WHERE kind = 'service' AND external_group_id IS NOT NULL AND status IN ('open', 'snoozed');

CREATE INDEX conversations_external_group
    ON conversations (external_group_id, last_message_at DESC)
    WHERE external_group_id IS NOT NULL;

CREATE UNIQUE INDEX conversations_external_chat_contact_unique
    ON conversations (tenant_id, channel_id, contact_id)
    WHERE kind = 'chat' AND channel_id IS NOT NULL AND contact_id IS NOT NULL;

COMMENT ON COLUMN conversations.audience IS '会话所属服务对象域：customer（外部客户）、partner（合作商家/商务）或 internal（内部员工）';
COMMENT ON COLUMN conversations.external_group_id IS '关联的外部群路由实体 ID（群内 @ 机器人服务型会话非空，与 contact_id 互斥）';

-- 清理历史商务单聊写入的 contact: 魔法键，单聊幂等由 conversations_external_chat_contact_unique 保障。
UPDATE chats
SET external_chat_id = ''
WHERE external_chat_id LIKE 'contact:%';

ALTER TABLE timeline_items
    ADD COLUMN reply_to_item_id uuid REFERENCES timeline_items (id) ON DELETE SET NULL;

COMMENT ON COLUMN timeline_items.reply_to_item_id IS '可选：该消息所引用回复的同会话时间线项 ID';

-- 工单关联目标从仅支持联系人升级为同时支持受影响联系人或外部群。
CREATE TABLE ticket_targets (
    id                     uuid PRIMARY KEY,
    created_at             timestamptz NOT NULL,
    updated_at             timestamptz NOT NULL,
    tenant_id              uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    ticket_id              uuid NOT NULL REFERENCES tickets (conversation_id) ON DELETE CASCADE,
    contact_id             uuid REFERENCES contacts (id) ON DELETE CASCADE,
    external_group_id      uuid REFERENCES external_groups (id) ON DELETE CASCADE,
    source_conversation_id uuid REFERENCES conversations (id) ON DELETE SET NULL,
    created_by             uuid REFERENCES members (id) ON DELETE SET NULL,
    CONSTRAINT ticket_targets_peer_xor CHECK ((contact_id IS NOT NULL) <> (external_group_id IS NOT NULL))
);

CREATE UNIQUE INDEX ticket_targets_ticket_contact_unique
    ON ticket_targets (tenant_id, ticket_id, contact_id)
    WHERE contact_id IS NOT NULL;

CREATE UNIQUE INDEX ticket_targets_ticket_group_unique
    ON ticket_targets (tenant_id, ticket_id, external_group_id)
    WHERE external_group_id IS NOT NULL;

CREATE INDEX ticket_targets_ticket_idx ON ticket_targets (tenant_id, ticket_id);
CREATE INDEX ticket_targets_contact_idx ON ticket_targets (tenant_id, contact_id) WHERE contact_id IS NOT NULL;
CREATE INDEX ticket_targets_group_idx ON ticket_targets (tenant_id, external_group_id) WHERE external_group_id IS NOT NULL;

COMMENT ON TABLE ticket_targets IS '工单与受影响对象（联系人或外部群）的多对多关联表（可选记录触发建单的来源服务型会话）';
COMMENT ON COLUMN ticket_targets.id IS '关联记录 ID（UUIDv7）';
COMMENT ON COLUMN ticket_targets.created_at IS '关联创建时间';
COMMENT ON COLUMN ticket_targets.updated_at IS '关联更新时间';
COMMENT ON COLUMN ticket_targets.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN ticket_targets.ticket_id IS '工单会话 ID（关联 tickets.conversation_id）';
COMMENT ON COLUMN ticket_targets.contact_id IS '受影响联系人 ID（与 external_group_id 互斥）';
COMMENT ON COLUMN ticket_targets.external_group_id IS '受影响外部群 ID（与 contact_id 互斥）';
COMMENT ON COLUMN ticket_targets.source_conversation_id IS '由哪条服务型会话转成此工单（可选）';
COMMENT ON COLUMN ticket_targets.created_by IS '建立关联的成员 ID（系统自动创建时为空）';

INSERT INTO ticket_targets (id, created_at, updated_at, tenant_id, ticket_id, contact_id, source_conversation_id, created_by)
SELECT id, created_at, updated_at, tenant_id, ticket_id, contact_id, source_conversation_id, created_by
FROM ticket_contacts;

DROP TABLE ticket_contacts;

-- +goose Down
CREATE TABLE ticket_contacts (
    id                     uuid PRIMARY KEY,
    created_at             timestamptz NOT NULL,
    updated_at             timestamptz NOT NULL,
    tenant_id              uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    ticket_id              uuid NOT NULL REFERENCES tickets (conversation_id) ON DELETE CASCADE,
    contact_id             uuid NOT NULL REFERENCES contacts (id) ON DELETE CASCADE,
    source_conversation_id uuid REFERENCES conversations (id) ON DELETE SET NULL,
    created_by             uuid REFERENCES members (id) ON DELETE SET NULL,
    CONSTRAINT ticket_contacts_unique UNIQUE (tenant_id, ticket_id, contact_id)
);

INSERT INTO ticket_contacts (id, created_at, updated_at, tenant_id, ticket_id, contact_id, source_conversation_id, created_by)
SELECT id, created_at, updated_at, tenant_id, ticket_id, contact_id, source_conversation_id, created_by
FROM ticket_targets
WHERE contact_id IS NOT NULL;

DROP TABLE ticket_targets;

ALTER TABLE timeline_items DROP COLUMN IF EXISTS reply_to_item_id;

DROP INDEX IF EXISTS conversations_external_chat_contact_unique;
DROP INDEX IF EXISTS conversations_external_group;
DROP INDEX IF EXISTS conversations_one_active_service_per_group;
DROP INDEX IF EXISTS conversations_one_active_service_per_contact;

ALTER TABLE conversations
    DROP CONSTRAINT IF EXISTS conversations_service_peer_xor,
    DROP COLUMN IF EXISTS external_group_id,
    DROP COLUMN IF EXISTS audience;

CREATE UNIQUE INDEX conversations_one_active_service_per_contact
    ON conversations (tenant_id, contact_id)
    WHERE kind = 'service' AND contact_id IS NOT NULL AND status IN ('open', 'snoozed');

DROP TABLE IF EXISTS external_users;
DROP TABLE IF EXISTS external_groups;

DROP INDEX IF EXISTS contacts_tenant_member;
ALTER TABLE contacts DROP COLUMN IF EXISTS member_id;

DROP INDEX IF EXISTS channels_tenant_audience;
ALTER TABLE channels DROP COLUMN IF EXISTS audience;
