-- +goose Up
CREATE TABLE ticket_types (
    id                 uuid PRIMARY KEY,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    tenant_id          uuid NOT NULL REFERENCES tenants (id),
    name               text NOT NULL,
    description        text NOT NULL DEFAULT '',
    default_team_id    uuid REFERENCES teams (id) ON DELETE RESTRICT,
    default_visibility text NOT NULL DEFAULT 'all' CHECK (default_visibility IN ('all', 'members')),
    position           integer NOT NULL DEFAULT 0,
    disabled_at        timestamptz
);

CREATE UNIQUE INDEX ticket_types_tenant_name ON ticket_types (tenant_id, lower(name));
CREATE INDEX ticket_types_tenant_pos ON ticket_types (tenant_id, position, created_at, id);
CREATE INDEX ticket_types_default_team ON ticket_types (tenant_id, default_team_id) WHERE default_team_id IS NOT NULL;

COMMENT ON TABLE ticket_types IS '工单类型：定义工作区中的工单模板、默认团队收件箱与默认可见范围';
COMMENT ON COLUMN ticket_types.id IS '主键';
COMMENT ON COLUMN ticket_types.created_at IS '创建时间';
COMMENT ON COLUMN ticket_types.updated_at IS '更新时间';
COMMENT ON COLUMN ticket_types.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN ticket_types.name IS '工单类型名称，同一工作区内忽略大小写唯一';
COMMENT ON COLUMN ticket_types.description IS '工单类型说明';
COMMENT ON COLUMN ticket_types.default_team_id IS '新建该类型工单时默认进入的团队收件箱 ID，可为空';
COMMENT ON COLUMN ticket_types.default_visibility IS '新建该类型工单的默认可见范围：all（客户可见）或 members（仅成员可见）';
COMMENT ON COLUMN ticket_types.position IS '展示排序序号';
COMMENT ON COLUMN ticket_types.disabled_at IS '非空表示已停用：不可用于新建工单，历史工单保留引用';

-- +goose Down
DROP TABLE ticket_types;
