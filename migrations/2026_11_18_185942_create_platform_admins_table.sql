-- +goose Up
CREATE TABLE platform_admins (
    id          uuid PRIMARY KEY,
    name        text NOT NULL,
    disabled_at timestamptz,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);
COMMENT ON TABLE platform_admins IS '平台管理员：负责整套部署，登录管理员端；邮箱和密码在 auth_* 表中';
COMMENT ON COLUMN platform_admins.disabled_at IS '非空表示已停用，不能再登录';

-- +goose Down
DROP TABLE platform_admins;
