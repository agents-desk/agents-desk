-- +goose Up
-- 成员关系从“当前快照”改为“带有效期的区间”：left_at 为空表示这个人现在仍在会话中。
-- 移出、退出不再删除记录，任意时间点的可见性和历史都能还原；重新加入是新增的一段。
-- 主键改为代理主键，同一个人可以有多段记录；仍用部分唯一索引保证“同时只有一段有效”。
-- 以 id 为主键后，表按约定以 id、created_at、updated_at 开头，因此重建表。

CREATE TABLE conversation_participants_new (
    id               uuid PRIMARY KEY,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    conversation_id  uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    participant_type text NOT NULL,
    participant_id   text NOT NULL,
    left_at          timestamptz
);

INSERT INTO conversation_participants_new (id, created_at, updated_at, conversation_id, participant_type, participant_id)
SELECT gen_random_uuid(), created_at, created_at, conversation_id, participant_type, participant_id
FROM conversation_participants;

DROP TABLE conversation_participants;
ALTER TABLE conversation_participants_new RENAME TO conversation_participants;

CREATE UNIQUE INDEX conversation_participants_active
    ON conversation_participants (conversation_id, participant_type, participant_id)
    WHERE left_at IS NULL;
CREATE INDEX conversation_participants_history
    ON conversation_participants (conversation_id, participant_type, participant_id, created_at);

COMMENT ON TABLE conversation_participants IS '会话参与者记录（联系人、成员等）：带有效期的区间，left_at 为空表示仍在会话中';
COMMENT ON COLUMN conversation_participants.id IS '主键';
COMMENT ON COLUMN conversation_participants.created_at IS '加入会话时间';
COMMENT ON COLUMN conversation_participants.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_participants.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_participants.participant_type IS '参与者类型（如 contact、member、bot）';
COMMENT ON COLUMN conversation_participants.participant_id IS '参与者 ID';
COMMENT ON COLUMN conversation_participants.left_at IS '离开会话的时间；为空表示仍在会话中';

-- +goose Down
-- 同一参与者的多段记录无法还原成唯一键：每段参与者只保留最近的一条有效记录
CREATE TABLE conversation_participants_old (
    conversation_id  uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    participant_type text NOT NULL,
    participant_id   text NOT NULL,
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, participant_type, participant_id)
);

INSERT INTO conversation_participants_old (conversation_id, participant_type, participant_id, created_at)
SELECT DISTINCT ON (conversation_id, participant_type, participant_id)
       conversation_id, participant_type, participant_id, created_at
FROM conversation_participants
ORDER BY conversation_id, participant_type, participant_id, (left_at IS NULL) DESC, created_at DESC, id DESC;

DROP TABLE conversation_participants;
ALTER TABLE conversation_participants_old RENAME TO conversation_participants;

COMMENT ON TABLE conversation_participants IS '会话参与者记录（联系人、成员等）';
COMMENT ON COLUMN conversation_participants.conversation_id IS '会话 ID';
COMMENT ON COLUMN conversation_participants.participant_type IS '参与者类型（如 contact、member）';
COMMENT ON COLUMN conversation_participants.participant_id IS '参与者 ID';
COMMENT ON COLUMN conversation_participants.created_at IS '加入会话时间';
