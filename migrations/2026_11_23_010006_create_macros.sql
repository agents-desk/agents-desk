-- +goose Up
CREATE TABLE macros (
    id         uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    tenant_id  uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    owner_id   uuid REFERENCES members (id) ON DELETE CASCADE,
    shortcut   text NOT NULL DEFAULT '',
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    actions    jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by uuid REFERENCES members (id) ON DELETE SET NULL
);
CREATE INDEX idx_macros_tenant_shared
    ON macros (tenant_id, updated_at DESC, id DESC)
    WHERE owner_id IS NULL;
CREATE INDEX idx_macros_tenant_personal
    ON macros (tenant_id, owner_id, updated_at DESC, id DESC)
    WHERE owner_id IS NOT NULL;
CREATE UNIQUE INDEX uq_macros_tenant_shared_shortcut
    ON macros (tenant_id, shortcut)
    WHERE owner_id IS NULL AND shortcut <> '';
CREATE UNIQUE INDEX uq_macros_tenant_personal_shortcut
    ON macros (tenant_id, owner_id, shortcut)
    WHERE owner_id IS NOT NULL AND shortcut <> '';
COMMENT ON TABLE macros IS '快捷回复模板，支持正文变量和附带会话操作；owner_id 为空表示工作区共享，非空表示成员个人';
COMMENT ON COLUMN macros.id IS '快捷回复 ID（UUIDv7）';
COMMENT ON COLUMN macros.created_at IS '创建时间';
COMMENT ON COLUMN macros.updated_at IS '最近修改时间';
COMMENT ON COLUMN macros.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN macros.owner_id IS '个人快捷回复的归属成员 ID；为空表示工作区共享';
COMMENT ON COLUMN macros.shortcut IS '可选快捷指令码（小写字母、数字、连字符和下划线），同作用域内唯一';
COMMENT ON COLUMN macros.title IS '快捷回复标题，用于列表展示与检索';
COMMENT ON COLUMN macros.body IS '回复模板正文，可包含 {{...}} 模板变量占位符';
COMMENT ON COLUMN macros.actions IS '附带执行的会话操作配置（JSON 对象）';
COMMENT ON COLUMN macros.created_by IS '创建该快捷回复的成员 ID';

-- +goose Down
DROP TABLE macros;
