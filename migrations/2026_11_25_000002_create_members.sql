-- +goose Up
CREATE TABLE members (
    id                       uuid PRIMARY KEY,
    created_at               timestamptz NOT NULL,
    updated_at               timestamptz NOT NULL,
    name                     text NOT NULL,
    disabled_at              timestamptz,
    avatar_file_id           uuid REFERENCES files (id),
    language                 text NOT NULL DEFAULT 'zh-CN',
    time_zone                text NOT NULL DEFAULT '',
    send_shortcut            text NOT NULL DEFAULT 'enter',
    open_next_after_reply    boolean NOT NULL DEFAULT false,
    notification_preferences jsonb NOT NULL DEFAULT '{}'::jsonb
);

COMMENT ON TABLE members IS '工作区成员的全局账号：一个人可以属于多个租户，在各租户中的角色在 authz_assignments 中；邮箱和密码在 auth_* 表中';
COMMENT ON COLUMN members.id IS '主键';
COMMENT ON COLUMN members.created_at IS '创建时间';
COMMENT ON COLUMN members.updated_at IS '更新时间';
COMMENT ON COLUMN members.name IS '显示名称';
COMMENT ON COLUMN members.disabled_at IS '非空表示已停用，不能再登录任何工作区';
COMMENT ON COLUMN members.avatar_file_id IS '头像文件 ID，关联 files.id；为空表示未设置头像';
COMMENT ON COLUMN members.language IS '界面与译文偏好语言（BCP 47 标签，如 zh-CN、en）';
COMMENT ON COLUMN members.time_zone IS 'IANA 时区名称；为空表示跟随浏览器时区';
COMMENT ON COLUMN members.send_shortcut IS '回复框发送快捷键：enter（回车发送）或 ctrl_enter（Ctrl/Cmd + 回车发送）';
COMMENT ON COLUMN members.open_next_after_reply IS '发送回复后是否自动打开下一个待回复会话';
COMMENT ON COLUMN members.notification_preferences IS '实时提醒偏好（JSON）：新会话指派、负责会话新消息、@ 提及的浏览器通知与提示音开关';

-- +goose Down
DROP TABLE members;
