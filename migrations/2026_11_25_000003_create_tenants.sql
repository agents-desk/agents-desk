-- +goose Up
CREATE TABLE tenants (
    id                  uuid PRIMARY KEY,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    slug                text NOT NULL,
    name                text NOT NULL,
    owner_id            uuid NOT NULL REFERENCES members (id),
    disabled_at         timestamptz,
    reopen_window_hours integer NOT NULL DEFAULT 24 CHECK (reopen_window_hours >= 0),
    next_contact_number bigint NOT NULL DEFAULT 1 CHECK (next_contact_number > 0),
    CONSTRAINT tenants_slug_unique UNIQUE (slug)
);

CREATE INDEX tenants_owner_id ON tenants (owner_id);
CREATE INDEX tenants_created_at ON tenants (created_at, id);

COMMENT ON TABLE tenants IS '租户，即产品中的工作区。id 是 authz 的域和业务表中的 tenant_id';
COMMENT ON COLUMN tenants.id IS '主键，同时作为 authz 域和业务表的 tenant_id';
COMMENT ON COLUMN tenants.created_at IS '创建时间';
COMMENT ON COLUMN tenants.updated_at IS '更新时间';
COMMENT ON COLUMN tenants.slug IS '地址：/t/{slug}/，创建后不能修改';
COMMENT ON COLUMN tenants.name IS '工作区名称';
COMMENT ON COLUMN tenants.owner_id IS '主管理员，同时在 authz 中拥有这个租户的 owner 角色，两者在同一个事务中修改';
COMMENT ON COLUMN tenants.disabled_at IS '非空表示已停用：成员不能进入，数据保留';
COMMENT ON COLUMN tenants.reopen_window_hours IS '单窗口渠道会话关闭后的重开窗口（小时），0 表示关闭后直接新建会话';
COMMENT ON COLUMN tenants.next_contact_number IS '工作区下一个新建联系人分配的递增编号';

-- +goose Down
DROP TABLE tenants;
