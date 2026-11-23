-- +goose Up
ALTER TABLE members
    ADD COLUMN send_shortcut            text NOT NULL DEFAULT 'enter',
    ADD COLUMN open_next_after_reply    boolean NOT NULL DEFAULT false,
    ADD COLUMN notification_preferences jsonb NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN members.send_shortcut IS '回复框发送快捷键：enter（回车发送）或 ctrl_enter（Ctrl/Cmd + 回车发送）';
COMMENT ON COLUMN members.open_next_after_reply IS '发送回复后是否自动打开下一个待回复会话';
COMMENT ON COLUMN members.notification_preferences IS '实时提醒偏好（JSON）：新会话指派、负责会话新消息、@ 提及的浏览器通知与提示音开关';

-- +goose Down
ALTER TABLE members
    DROP COLUMN notification_preferences,
    DROP COLUMN open_next_after_reply,
    DROP COLUMN send_shortcut;
