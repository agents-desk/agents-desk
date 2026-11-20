-- +goose Up
-- 联系人：和工作区沟通的一个外部的人。用户是产品传来了身份的客户，线索是以访客身份发起会话的人。
CREATE TABLE contacts (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    kind        text NOT NULL,
    name        text NOT NULL DEFAULT '',
    email       text NOT NULL DEFAULT '',
    external_user_id text NOT NULL DEFAULT '',
    language    text NOT NULL DEFAULT '',
    attributes  jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX contacts_tenant ON contacts (tenant_id, created_at);
CREATE UNIQUE INDEX contacts_tenant_user ON contacts (tenant_id, external_user_id) WHERE external_user_id <> '';

-- 渠道：一个具体的接入点，例如某个网站上的聊天插件。kind 决定由哪个连接器收发消息。
CREATE TABLE channels (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    kind        text NOT NULL,
    name        text NOT NULL,
    settings    jsonb NOT NULL DEFAULT '{}',
    team_id     uuid REFERENCES teams (id) ON DELETE SET NULL,
    enabled     boolean NOT NULL DEFAULT true
);
CREATE INDEX channels_tenant ON channels (tenant_id);

-- 联系人在渠道中的身份：同一个渠道中，一个外部编号对应一个联系人。
CREATE TABLE contact_identities (
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    channel_id  uuid NOT NULL REFERENCES channels (id),
    external_id text NOT NULL,
    contact_id  uuid NOT NULL REFERENCES contacts (id),
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (channel_id, external_id)
);
CREATE INDEX contact_identities_contact ON contact_identities (contact_id);

-- 会话。kind 是 service（服务型）或 chat（聊天型）；服务型会话有团队收件箱、负责人和状态。
-- waiting_since 是对方的消息开始等待回复的时间，成员或 AI 回复后清空；last_seq 是时间线中最新一项的序号。
CREATE TABLE conversations (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    kind            text NOT NULL,
    channel_id      uuid REFERENCES channels (id),
    contact_id      uuid REFERENCES contacts (id),
    team_id         uuid REFERENCES teams (id) ON DELETE SET NULL,
    assignee_type   text NOT NULL DEFAULT '',
    assignee_id     text NOT NULL DEFAULT '',
    status          text NOT NULL,
    snoozed_until   timestamptz,
    priority        text NOT NULL DEFAULT 'normal',
    language        text NOT NULL DEFAULT '',
    waiting_since   timestamptz,
    first_reply_at  timestamptz,
    closed_at       timestamptz,
    last_message_at timestamptz NOT NULL,
    last_seq        bigint NOT NULL DEFAULT 0,
    attributes      jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX conversations_tenant_status ON conversations (tenant_id, status, last_message_at DESC);
CREATE INDEX conversations_contact ON conversations (contact_id, last_message_at DESC);

-- 会话的参与者：联系人、成员，以后还有外部群中的人、机器人、AI。
CREATE TABLE conversation_participants (
    conversation_id  uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    participant_type text NOT NULL,
    participant_id   text NOT NULL,
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, participant_type, participant_id)
);

-- 时间线：会话中只追加的记录。kind 是 message、note 或 event；visibility 是 all（所有人）或 members（仅成员）。
-- content 是内容，derived 是后台生成的派生内容（例如按语言的译文）。渠道中的同一条消息只记录一次。
CREATE TABLE timeline_items (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    seq             bigint NOT NULL,
    kind            text NOT NULL,
    actor_type      text NOT NULL,
    actor_id        text NOT NULL DEFAULT '',
    visibility      text NOT NULL,
    content         jsonb NOT NULL,
    derived         jsonb NOT NULL DEFAULT '{}',
    channel_id      uuid REFERENCES channels (id),
    external_id     text,
    UNIQUE (conversation_id, seq)
);
CREATE UNIQUE INDEX timeline_items_external ON timeline_items (channel_id, external_id) WHERE external_id IS NOT NULL;

-- 消息的送达状态：sending、delivered、read、failed，以及发出后在渠道中的编号。时间线保持只追加，送达状态单独记录。
CREATE TABLE message_deliveries (
    item_id     uuid PRIMARY KEY REFERENCES timeline_items (id) ON DELETE CASCADE,
    status      text NOT NULL,
    external_id text NOT NULL DEFAULT '',
    error       text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL
);

-- +goose Down
DROP TABLE message_deliveries;
DROP TABLE timeline_items;
DROP TABLE conversation_participants;
DROP TABLE conversations;
DROP TABLE contact_identities;
DROP TABLE channels;
DROP TABLE contacts;
