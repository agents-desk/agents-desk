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

CREATE TABLE conversation_tags (
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    tag_id          uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, tag_id)
);
CREATE INDEX conversation_tags_tenant_tag ON conversation_tags (tenant_id, tag_id, conversation_id);
COMMENT ON TABLE conversation_tags IS '会话与标签的多对多关联';
COMMENT ON COLUMN conversation_tags.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_tags.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_tags.tag_id IS '标签 ID';
COMMENT ON COLUMN conversation_tags.created_at IS '打标时间';

CREATE TABLE contact_tags (
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    contact_id uuid NOT NULL REFERENCES contacts (id) ON DELETE CASCADE,
    tag_id     uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (contact_id, tag_id)
);
CREATE INDEX contact_tags_tenant_tag ON contact_tags (tenant_id, tag_id, contact_id);
COMMENT ON TABLE contact_tags IS '联系人与标签的多对多关联';
COMMENT ON COLUMN contact_tags.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN contact_tags.contact_id IS '联系人 ID';
COMMENT ON COLUMN contact_tags.tag_id IS '标签 ID';
COMMENT ON COLUMN contact_tags.created_at IS '打标时间';

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
DROP TABLE contact_tags;
DROP TABLE conversation_tags;
DROP TABLE tags;
