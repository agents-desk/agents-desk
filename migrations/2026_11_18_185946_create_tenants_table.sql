-- +goose Up
CREATE TABLE tenants (
    id          uuid PRIMARY KEY,
    slug        text NOT NULL,
    name        text NOT NULL,
    owner_id    uuid NOT NULL REFERENCES members (id),
    disabled_at timestamptz,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    CONSTRAINT tenants_slug_unique UNIQUE (slug)
);
CREATE INDEX tenants_owner_id ON tenants (owner_id);
CREATE INDEX tenants_created_at ON tenants (created_at, id);
COMMENT ON TABLE tenants IS '租户，即产品中的工作区。id 是 authz 的域和业务表中的 tenant_id';
COMMENT ON COLUMN tenants.slug IS '地址：/t/{slug}/，创建后不能修改';
COMMENT ON COLUMN tenants.owner_id IS '主管理员，同时在 authz 中拥有这个租户的 owner 角色，两者在同一个事务中修改';
COMMENT ON COLUMN tenants.disabled_at IS '非空表示已停用：成员不能进入，数据保留';

-- +goose Down
DROP TABLE tenants;
