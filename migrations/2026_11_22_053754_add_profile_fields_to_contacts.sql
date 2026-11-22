-- +goose Up
-- 联系人的资料字段：备注、所在地、时区、来源页面和最近活跃时间。
ALTER TABLE contacts
    ADD COLUMN notes        text NOT NULL DEFAULT '',
    ADD COLUMN location     text NOT NULL DEFAULT '',
    ADD COLUMN time_zone    text NOT NULL DEFAULT '',
    ADD COLUMN referrer     text NOT NULL DEFAULT '',
    ADD COLUMN last_seen_at timestamptz;

CREATE INDEX contacts_tenant_kind ON contacts (tenant_id, kind, created_at DESC);
CREATE INDEX contacts_tenant_last_seen ON contacts (tenant_id, COALESCE(last_seen_at, created_at) DESC, id DESC);

-- +goose Down
DROP INDEX contacts_tenant_last_seen;
DROP INDEX contacts_tenant_kind;

ALTER TABLE contacts
    DROP COLUMN last_seen_at,
    DROP COLUMN referrer,
    DROP COLUMN time_zone,
    DROP COLUMN location,
    DROP COLUMN notes;
