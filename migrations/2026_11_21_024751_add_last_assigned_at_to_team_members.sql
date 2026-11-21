-- +goose Up
-- 轮流分配时这位成员最近一次在这个团队收件箱中分到会话的时间；从来没有分到过时为空，排在最前。
ALTER TABLE team_members ADD COLUMN last_assigned_at timestamptz;

-- +goose Down
ALTER TABLE team_members DROP COLUMN last_assigned_at;
