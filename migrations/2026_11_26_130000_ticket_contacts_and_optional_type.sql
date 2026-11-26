-- +goose Up
-- 允许工单不选工单类型（通用工单），并把工单关联从会话级（conversation_links）提升到联系人级（ticket_contacts）。
ALTER TABLE tickets ALTER COLUMN type_id DROP NOT NULL;

CREATE TABLE ticket_contacts (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    ticket_id uuid NOT NULL REFERENCES tickets(conversation_id) ON DELETE CASCADE,
    contact_id uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    source_conversation_id uuid REFERENCES conversations(id) ON DELETE SET NULL,
    created_by uuid REFERENCES members(id) ON DELETE SET NULL,
    CONSTRAINT ticket_contacts_unique UNIQUE (tenant_id, ticket_id, contact_id)
);

CREATE INDEX ticket_contacts_ticket_idx ON ticket_contacts (tenant_id, ticket_id);
CREATE INDEX ticket_contacts_contact_idx ON ticket_contacts (tenant_id, contact_id);

COMMENT ON TABLE ticket_contacts IS '工单与受影响联系人的多对多关联表（可选记录触发建单的来源服务型会话）';
COMMENT ON COLUMN ticket_contacts.id IS '关联记录 ID（UUIDv7）';
COMMENT ON COLUMN ticket_contacts.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN ticket_contacts.ticket_id IS '工单会话 ID（关联 tickets.conversation_id）';
COMMENT ON COLUMN ticket_contacts.contact_id IS '受影响联系人 ID（关联 contacts.id）';
COMMENT ON COLUMN ticket_contacts.source_conversation_id IS '由哪条服务型会话转成此工单（可选）';
COMMENT ON COLUMN ticket_contacts.created_by IS '建立关联的成员 ID（系统自动创建时为空）';
COMMENT ON COLUMN ticket_contacts.created_at IS '关联创建时间';
COMMENT ON COLUMN ticket_contacts.updated_at IS '关联更新时间';

INSERT INTO ticket_contacts (id, tenant_id, ticket_id, contact_id, source_conversation_id, created_by, created_at, updated_at)
SELECT DISTINCT ON (cl.tenant_id, cl.ticket_id, sc.contact_id)
    cl.id,
    cl.tenant_id,
    cl.ticket_id,
    sc.contact_id,
    CASE WHEN cl.relation = 'source' THEN cl.conversation_id ELSE NULL END,
    cl.created_by,
    cl.created_at,
    cl.updated_at
FROM conversation_links cl
JOIN conversations sc ON sc.id = cl.conversation_id AND sc.tenant_id = cl.tenant_id
WHERE sc.contact_id IS NOT NULL
ORDER BY cl.tenant_id, cl.ticket_id, sc.contact_id, (cl.relation = 'source') DESC, cl.created_at ASC, cl.id ASC;

DROP TABLE conversation_links;

-- +goose Down
CREATE TABLE conversation_links (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    ticket_id uuid NOT NULL REFERENCES tickets(conversation_id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    relation text NOT NULL CHECK (relation IN ('source', 'linked')),
    created_by uuid REFERENCES members(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT conversation_links_unique UNIQUE (tenant_id, ticket_id, conversation_id)
);

CREATE INDEX conversation_links_ticket_idx ON conversation_links (tenant_id, ticket_id);
CREATE INDEX conversation_links_conversation_idx ON conversation_links (tenant_id, conversation_id);

COMMENT ON TABLE conversation_links IS '工单与服务型会话的多对多关联表';
COMMENT ON COLUMN conversation_links.id IS '关联记录 ID（UUIDv7）';
COMMENT ON COLUMN conversation_links.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_links.ticket_id IS '工单会话 ID（关联 tickets.conversation_id）';
COMMENT ON COLUMN conversation_links.conversation_id IS '服务型会话 ID（关联 conversations.id）';
COMMENT ON COLUMN conversation_links.relation IS '关联来源：source（由会话转成工单）或 linked（手动关联到已有工单）';
COMMENT ON COLUMN conversation_links.created_by IS '建立关联的成员 ID（系统自动创建时为空）';
COMMENT ON COLUMN conversation_links.created_at IS '关联创建时间';
COMMENT ON COLUMN conversation_links.updated_at IS '关联更新时间';

INSERT INTO conversation_links (id, tenant_id, ticket_id, conversation_id, relation, created_by, created_at, updated_at)
SELECT
    tc.id,
    tc.tenant_id,
    tc.ticket_id,
    COALESCE(tc.source_conversation_id, (
        SELECT sc.id FROM conversations sc
        WHERE sc.tenant_id = tc.tenant_id AND sc.kind = 'service' AND sc.contact_id = tc.contact_id
        ORDER BY sc.last_message_at DESC, sc.id DESC LIMIT 1
    )),
    CASE WHEN tc.source_conversation_id IS NOT NULL THEN 'source' ELSE 'linked' END,
    tc.created_by,
    tc.created_at,
    tc.updated_at
FROM ticket_contacts tc
WHERE tc.source_conversation_id IS NOT NULL OR EXISTS (
    SELECT 1 FROM conversations sc
    WHERE sc.tenant_id = tc.tenant_id AND sc.kind = 'service' AND sc.contact_id = tc.contact_id
)
ON CONFLICT DO NOTHING;

DROP TABLE ticket_contacts;
DELETE FROM tickets WHERE type_id IS NULL;
ALTER TABLE tickets ALTER COLUMN type_id SET NOT NULL;
