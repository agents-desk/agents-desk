-- +goose Up
CREATE TABLE platform_admins (
    id             uuid PRIMARY KEY,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    name           text NOT NULL,
    disabled_at    timestamptz,
    avatar_file_id uuid REFERENCES files (id)
);

COMMENT ON TABLE platform_admins IS '平台管理员：负责整套部署，登录管理员端；邮箱和密码在 auth_* 表中';
COMMENT ON COLUMN platform_admins.id IS '主键';
COMMENT ON COLUMN platform_admins.created_at IS '创建时间';
COMMENT ON COLUMN platform_admins.updated_at IS '更新时间';
COMMENT ON COLUMN platform_admins.name IS '显示名称';
COMMENT ON COLUMN platform_admins.disabled_at IS '非空表示已停用，不能再登录';
COMMENT ON COLUMN platform_admins.avatar_file_id IS '头像文件 ID，关联 files.id；为空表示未设置头像';

-- +goose Down
DROP TABLE platform_admins;
