-- +goose Up
-- 客户对会话的评价：rating 是 1 到 5 星，rating_comment 是附的一句意见，rated_at 是评价的时间。
-- 会话每关闭一次可以评价一次：rated_at 早于 closed_at 时，这次关闭还没有评价。
ALTER TABLE conversations ADD COLUMN rating smallint;
ALTER TABLE conversations ADD COLUMN rating_comment text NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN rated_at timestamptz;

-- +goose Down
ALTER TABLE conversations DROP COLUMN rated_at;
ALTER TABLE conversations DROP COLUMN rating_comment;
ALTER TABLE conversations DROP COLUMN rating;
