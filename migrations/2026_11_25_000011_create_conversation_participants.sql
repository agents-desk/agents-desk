-- +goose Up
CREATE TABLE conversation_participants (
    conversation_id  uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    participant_type text NOT NULL,
    participant_id   text NOT NULL,
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, participant_type, participant_id)
);

COMMENT ON TABLE conversation_participants IS '会话参与者记录（联系人、成员等）';
COMMENT ON COLUMN conversation_participants.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_participants.participant_type IS '参与者类型（如 contact、member）';
COMMENT ON COLUMN conversation_participants.participant_id IS '参与者 ID';
COMMENT ON COLUMN conversation_participants.created_at IS '加入会话时间';

-- +goose Down
DROP TABLE conversation_participants;
