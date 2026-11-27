-- +goose Up
-- 运营上线护栏：坐席在线心跳、联系人拉黑、垃圾会话标记与工作区 IP 黑名单。

ALTER TABLE member_statuses
    ADD COLUMN last_seen_at TIMESTAMPTZ;

COMMENT ON COLUMN member_statuses.last_seen_at IS '坐席在线状态的最近心跳时间';

CREATE INDEX member_statuses_online_seen_idx
    ON member_statuses (last_seen_at)
    WHERE status = 'online';

ALTER TABLE contacts
    ADD COLUMN blocked_at TIMESTAMPTZ,
    ADD COLUMN blocked_reason TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN contacts.blocked_at IS '联系人拉黑时间，非空表示已拉黑';
COMMENT ON COLUMN contacts.blocked_reason IS '联系人拉黑原因';

CREATE INDEX contacts_tenant_blocked_idx
    ON contacts (tenant_id, blocked_at DESC)
    WHERE blocked_at IS NOT NULL;

ALTER TABLE conversations
    ADD COLUMN spam_at TIMESTAMPTZ;

COMMENT ON COLUMN conversations.spam_at IS '标记为垃圾会话的时间，非空表示垃圾会话并排除出常规收件箱与报表';

CREATE INDEX conversations_tenant_spam_idx
    ON conversations (tenant_id, spam_at DESC)
    WHERE spam_at IS NOT NULL;

CREATE TABLE blocked_ips (
    id UUID PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cidr INET NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_by UUID
);

COMMENT ON TABLE blocked_ips IS '工作区访客 IP 与网段黑名单';
COMMENT ON COLUMN blocked_ips.id IS '黑名单规则唯一标识';
COMMENT ON COLUMN blocked_ips.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN blocked_ips.cidr IS '封禁的单 IP 或 CIDR 网段';
COMMENT ON COLUMN blocked_ips.reason IS '封禁原因说明';
COMMENT ON COLUMN blocked_ips.created_by IS '创建该规则的工作区成员 ID';
COMMENT ON COLUMN blocked_ips.created_at IS '创建时间';
COMMENT ON COLUMN blocked_ips.updated_at IS '更新时间';

CREATE UNIQUE INDEX blocked_ips_tenant_cidr_unique
    ON blocked_ips (tenant_id, cidr);

CREATE INDEX blocked_ips_tenant_created_idx
    ON blocked_ips (tenant_id, created_at DESC);
