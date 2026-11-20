-- +goose Up
-- 团队收件箱单独设置的时区和每周的工作时间（hours.Schedule 的 JSON）；为空表示使用工作区的工作时间。
ALTER TABLE teams ADD COLUMN business_hours jsonb;

-- +goose Down
ALTER TABLE teams DROP COLUMN business_hours;
