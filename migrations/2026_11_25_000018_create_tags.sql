-- +goose Up
CREATE TABLE tags (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    name        text NOT NULL,
    color       text NOT NULL DEFAULT 'blue-gray',
    description text NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX tags_tenant_name ON tags (tenant_id, lower(name));

COMMENT ON TABLE tags IS '工作区标签：用于标记和筛选会话与联系人';
COMMENT ON COLUMN tags.id IS '主键';
COMMENT ON COLUMN tags.created_at IS '创建时间';
COMMENT ON COLUMN tags.updated_at IS '更新时间';
COMMENT ON COLUMN tags.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN tags.name IS '标签名称，同一工作区内不区分大小写唯一';
COMMENT ON COLUMN tags.color IS '标签展示颜色名（Mantine 主题预设色）';
COMMENT ON COLUMN tags.description IS '标签说明';

-- +goose Down
DROP TABLE tags;
