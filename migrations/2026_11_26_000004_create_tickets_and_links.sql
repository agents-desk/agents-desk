-- +goose Up
CREATE TABLE tickets (
    conversation_id uuid PRIMARY KEY REFERENCES conversations (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    number          bigint NOT NULL CHECK (number > 0),
    type_id         uuid NOT NULL REFERENCES ticket_types (id) ON DELETE RESTRICT,
    title           text NOT NULL,
    description     text NOT NULL DEFAULT '',
    visibility      text NOT NULL CHECK (visibility IN ('all', 'members')),
    created_by      uuid REFERENCES members (id),
    resolved_at     timestamptz
);

CREATE UNIQUE INDEX tickets_tenant_number ON tickets (tenant_id, number);
CREATE INDEX tickets_tenant_type ON tickets (tenant_id, type_id);
CREATE INDEX tickets_tenant_created_by ON tickets (tenant_id, created_by) WHERE created_by IS NOT NULL;

COMMENT ON TABLE tickets IS '工单扩展表：与 conversations（kind=ticket）一对一关联，存储工单编号、类型、标题、描述、可见范围与解决时间';
COMMENT ON COLUMN tickets.conversation_id IS '主键，关联 conversations.id（kind=ticket）';
COMMENT ON COLUMN tickets.created_at IS '创建时间';
COMMENT ON COLUMN tickets.updated_at IS '更新时间';
COMMENT ON COLUMN tickets.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN tickets.number IS '工单在工作区内的递增编号，从 1 开始，创建后不变';
COMMENT ON COLUMN tickets.type_id IS '所属工单类型 ID，关联 ticket_types.id';
COMMENT ON COLUMN tickets.title IS '工单标题';
COMMENT ON COLUMN tickets.description IS '工单详细描述';
COMMENT ON COLUMN tickets.visibility IS '工单可见范围：all（客户可见）或 members（仅工作区成员可见）';
COMMENT ON COLUMN tickets.created_by IS '创建该工单的成员 ID；系统创建时为空';
COMMENT ON COLUMN tickets.resolved_at IS '工单进入已解决状态的时间；重新回到未解决状态时清空';

CREATE TABLE conversation_links (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    ticket_id       uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    relation        text NOT NULL CHECK (relation IN ('source', 'linked')),
    created_by      uuid REFERENCES members (id),
    CONSTRAINT conversation_links_unique UNIQUE (ticket_id, conversation_id),
    CONSTRAINT conversation_links_distinct CHECK (ticket_id <> conversation_id)
);

CREATE INDEX conversation_links_tenant_ticket ON conversation_links (tenant_id, ticket_id, created_at DESC);
CREATE INDEX conversation_links_tenant_conv ON conversation_links (tenant_id, conversation_id, created_at DESC);

COMMENT ON TABLE conversation_links IS '会话关联表：多对多连接工单（kind=ticket）与服务型会话（kind=service）';
COMMENT ON COLUMN conversation_links.id IS '主键';
COMMENT ON COLUMN conversation_links.created_at IS '关联创建时间';
COMMENT ON COLUMN conversation_links.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_links.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_links.ticket_id IS '工单会话 ID（conversations.id，kind=ticket）';
COMMENT ON COLUMN conversation_links.conversation_id IS '关联的服务型会话 ID（conversations.id，kind=service）';
COMMENT ON COLUMN conversation_links.relation IS '关联来源：source（由该会话转成工单）或 linked（手动关联到已有工单）';
COMMENT ON COLUMN conversation_links.created_by IS '建立该关联的成员 ID；系统建立时为空';

ALTER TABLE conversations
    ADD CONSTRAINT conversations_kind_status_check CHECK (
        (kind = 'service' AND status IN ('open', 'snoozed', 'closed'))
        OR (kind = 'ticket' AND status IN ('submitted', 'in_progress', 'waiting_on_customer', 'resolved'))
        OR (kind = 'chat')
    );

COMMENT ON COLUMN conversations.kind IS '会话类型：service（服务型）、ticket（工单）或 chat（聊天型）';
COMMENT ON COLUMN conversations.status IS '会话状态：服务型为 open/snoozed/closed；工单为 submitted/in_progress/waiting_on_customer/resolved';

-- +goose Down
ALTER TABLE conversations
    DROP CONSTRAINT conversations_kind_status_check;

DROP TABLE conversation_links;
DROP TABLE tickets;
