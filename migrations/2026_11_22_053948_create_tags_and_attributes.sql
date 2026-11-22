-- +goose Up
-- 标签：工作区统一的标签定义，可以打在会话和联系人上。同一工作区内按小写不重名。
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

-- 会话与标签的关联。删除标签或会话时自动清理关联。
CREATE TABLE conversation_tags (
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    tag_id          uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, tag_id)
);
CREATE INDEX conversation_tags_tenant_tag ON conversation_tags (tenant_id, tag_id, conversation_id);

-- 联系人与标签的关联。删除标签或联系人时自动清理关联。
CREATE TABLE contact_tags (
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    contact_id uuid NOT NULL REFERENCES contacts (id) ON DELETE CASCADE,
    tag_id     uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (contact_id, tag_id)
);
CREATE INDEX contact_tags_tenant_tag ON contact_tags (tenant_id, tag_id, contact_id);

-- 自定义属性定义：target 是属性所属的对象（conversation、contact，后续扩展 ticket、organization）。
-- key 在同一工作区和对象下唯一，创建后不可修改；options 在 type 为 select/multi_select 时保存可选项列表。
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

-- +goose Down
DROP TABLE attribute_definitions;
DROP TABLE contact_tags;
DROP TABLE conversation_tags;
DROP TABLE tags;
