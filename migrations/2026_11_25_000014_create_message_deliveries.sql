-- +goose Up
CREATE TABLE message_deliveries (
    item_id     uuid PRIMARY KEY REFERENCES timeline_items (id) ON DELETE CASCADE,
    status      text NOT NULL,
    external_id text NOT NULL DEFAULT '',
    error       text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL
);

COMMENT ON TABLE message_deliveries IS '发出消息的渠道送达状态记录';
COMMENT ON COLUMN message_deliveries.item_id IS '关联的时间线条目 ID，主键';
COMMENT ON COLUMN message_deliveries.status IS '送达状态：sending、delivered、read 或 failed';
COMMENT ON COLUMN message_deliveries.external_id IS '消息发出后渠道返回的外部编号';
COMMENT ON COLUMN message_deliveries.error IS '发送失败时的错误说明';
COMMENT ON COLUMN message_deliveries.updated_at IS '状态更新时间';

-- +goose Down
DROP TABLE message_deliveries;
