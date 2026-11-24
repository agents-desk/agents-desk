-- +goose Up
CREATE TABLE conversation_reads (
    member_id       uuid NOT NULL REFERENCES members (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    last_read_seq   bigint NOT NULL,
    updated_at      timestamptz NOT NULL,
    PRIMARY KEY (member_id, conversation_id)
);

COMMENT ON TABLE conversation_reads IS '成员在各会话中的已读进度';
COMMENT ON COLUMN conversation_reads.member_id IS '成员 ID';
COMMENT ON COLUMN conversation_reads.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_reads.last_read_seq IS '已读到的最大时间线序号';
COMMENT ON COLUMN conversation_reads.updated_at IS '已读进度更新时间';

-- +goose Down
DROP TABLE conversation_reads;
