-- +goose Up
CREATE TABLE members (
    id          uuid PRIMARY KEY,
    name        text NOT NULL,
    disabled_at timestamptz,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);
COMMENT ON TABLE members IS '工作区成员的全局账号：一个人可以属于多个租户，在各租户中的角色在 authz_assignments 中；邮箱和密码在 auth_* 表中';
COMMENT ON COLUMN members.disabled_at IS '非空表示已停用，不能再登录任何工作区';

-- +goose Down
DROP TABLE members;
