-- +goose Up
ALTER TABLE conversations
    ADD COLUMN reply_channel_id uuid REFERENCES channels (id);

UPDATE conversations
SET reply_channel_id = channel_id
WHERE channel_id IS NOT NULL;

CREATE INDEX conversations_tenant_reply_channel
    ON conversations (tenant_id, reply_channel_id)
    WHERE reply_channel_id IS NOT NULL;

COMMENT ON COLUMN conversations.reply_channel_id IS '当前默认回复渠道 ID，随对方最新发消息的渠道自动更新；为空时回退使用 channel_id';

-- +goose Down
DROP INDEX IF EXISTS conversations_tenant_reply_channel;

ALTER TABLE conversations
    DROP COLUMN IF EXISTS reply_channel_id;
