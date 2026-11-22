-- +goose Up
-- 最近一次关闭后向客户发出评价邀请的时间；为空或早于 closed_at 时这一轮不请客户评价。
ALTER TABLE conversations ADD COLUMN rating_requested_at timestamptz;

-- +goose Down
ALTER TABLE conversations DROP COLUMN rating_requested_at;
