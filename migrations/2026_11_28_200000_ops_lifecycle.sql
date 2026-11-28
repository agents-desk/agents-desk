-- +goose Up
ALTER TABLE channels
    ADD COLUMN archived_at timestamptz;

COMMENT ON COLUMN channels.archived_at IS '渠道归档时间，保留历史会话与报表关联；未归档时为空';

CREATE INDEX channels_tenant_active
    ON channels (tenant_id, kind)
    WHERE archived_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS channels_tenant_active;

ALTER TABLE channels
    DROP COLUMN IF EXISTS archived_at;
