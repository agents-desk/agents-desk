-- +goose Up
CREATE TABLE attribute_definitions (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    target      text NOT NULL,
    key         text NOT NULL,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    type        text NOT NULL,
    options     jsonb NOT NULL DEFAULT '[]',
    position    integer NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX attribute_definitions_tenant_target_key ON attribute_definitions (tenant_id, target, key);
CREATE INDEX attribute_definitions_tenant_target_pos ON attribute_definitions (tenant_id, target, position, created_at);

COMMENT ON TABLE attribute_definitions IS '自定义属性定义：配置会话或联系人的扩展字段';
COMMENT ON COLUMN attribute_definitions.id IS '主键';
COMMENT ON COLUMN attribute_definitions.created_at IS '创建时间';
COMMENT ON COLUMN attribute_definitions.updated_at IS '更新时间';
COMMENT ON COLUMN attribute_definitions.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN attribute_definitions.target IS '属性所属对象类型（如 conversation、contact）';
COMMENT ON COLUMN attribute_definitions.key IS '属性键名，同一工作区和对象下唯一且创建后不可改';
COMMENT ON COLUMN attribute_definitions.name IS '属性显示名称';
COMMENT ON COLUMN attribute_definitions.description IS '属性说明';
COMMENT ON COLUMN attribute_definitions.type IS '属性数据类型（如 text、number、boolean、date、select、multi_select）';
COMMENT ON COLUMN attribute_definitions.options IS '下拉单选或多选属性的可选项列表（JSON 数组）';
COMMENT ON COLUMN attribute_definitions.position IS '展示排序序号';

-- +goose Down
DROP TABLE attribute_definitions;
