-- +goose Up
-- 联系人的资料字段：备注、所在地、时区、来源页面和最近活跃时间。
ALTER TABLE contacts
    ADD COLUMN notes        text NOT NULL DEFAULT '',
    ADD COLUMN location     text NOT NULL DEFAULT '',
    ADD COLUMN time_zone    text NOT NULL DEFAULT '',
    ADD COLUMN referrer     text NOT NULL DEFAULT '',
    ADD COLUMN last_seen_at timestamptz;

COMMENT ON COLUMN contacts.notes IS '成员填写的联系人内部备注，仅成员可见';
COMMENT ON COLUMN contacts.location IS '联系人所在地';
COMMENT ON COLUMN contacts.time_zone IS '联系人所在的 IANA 时区名，用于显示对方当地时间';
COMMENT ON COLUMN contacts.referrer IS '联系人首次访问的来源页面地址';
COMMENT ON COLUMN contacts.last_seen_at IS '联系人最近活跃或联系的时间';

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
