-- +goose Up
-- 成员的偏好设置，属于账号本身，在所有工作区中一样。
-- language：语言，现在决定翻译客户消息的目标语言；time_zone：IANA 时区名，为空表示跟随浏览器。
ALTER TABLE members ADD COLUMN language text NOT NULL DEFAULT 'zh-CN';
ALTER TABLE members ADD COLUMN time_zone text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE members DROP COLUMN time_zone;
ALTER TABLE members DROP COLUMN language;
