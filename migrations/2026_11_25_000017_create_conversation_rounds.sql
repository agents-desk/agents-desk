-- +goose Up
CREATE TABLE conversation_rounds (
    id                  uuid PRIMARY KEY,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    tenant_id           uuid NOT NULL REFERENCES tenants (id),
    conversation_id     uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    round               integer NOT NULL,
    team_id             uuid REFERENCES teams (id) ON DELETE RESTRICT,
    assignee_type       text NOT NULL DEFAULT '',
    assignee_id         text NOT NULL DEFAULT '',
    first_reply_by_type text NOT NULL DEFAULT '',
    first_reply_by_id   text NOT NULL DEFAULT '',
    closed_by_type      text NOT NULL DEFAULT '',
    closed_by_id        text NOT NULL DEFAULT '',
    opened_at           timestamptz NOT NULL,
    first_reply_at      timestamptz,
    closed_at           timestamptz,
    rating              smallint,
    rating_comment      text NOT NULL DEFAULT '',
    rated_at            timestamptz,
    UNIQUE (conversation_id, round)
);

CREATE INDEX conversation_rounds_tenant_opened ON conversation_rounds (tenant_id, opened_at DESC);
CREATE INDEX conversation_rounds_tenant_first_reply ON conversation_rounds (tenant_id, first_reply_at DESC) WHERE first_reply_at IS NOT NULL;
CREATE INDEX conversation_rounds_tenant_closed ON conversation_rounds (tenant_id, closed_at DESC) WHERE closed_at IS NOT NULL;
CREATE INDEX conversation_rounds_tenant_rated ON conversation_rounds (tenant_id, rated_at DESC) WHERE rated_at IS NOT NULL;
CREATE INDEX conversation_rounds_tenant_team ON conversation_rounds (tenant_id, team_id, opened_at DESC);
CREATE INDEX conversation_rounds_tenant_assignee ON conversation_rounds (tenant_id, assignee_id, closed_at DESC) WHERE closed_at IS NOT NULL;
CREATE INDEX conversation_rounds_tenant_first_replier ON conversation_rounds (tenant_id, first_reply_by_id, first_reply_at DESC) WHERE first_reply_at IS NOT NULL;

COMMENT ON TABLE conversation_rounds IS '服务型会话的每一轮服务记录，从打开或重新打开开始，记录首次回复、关闭归属快照与客户评价';
COMMENT ON COLUMN conversation_rounds.id IS '服务轮次 ID（UUIDv7）';
COMMENT ON COLUMN conversation_rounds.created_at IS '创建时间';
COMMENT ON COLUMN conversation_rounds.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_rounds.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_rounds.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN conversation_rounds.round IS '会话内第几轮服务，从 1 递增';
COMMENT ON COLUMN conversation_rounds.team_id IS '本轮归属的团队收件箱 ID（关闭后保留快照）';
COMMENT ON COLUMN conversation_rounds.assignee_type IS '本轮负责人类型，为空表示无负责人';
COMMENT ON COLUMN conversation_rounds.assignee_id IS '本轮负责人编号';
COMMENT ON COLUMN conversation_rounds.first_reply_by_type IS '本轮首次回复的发起者类型，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.first_reply_by_id IS '本轮首次回复的发起者编号，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.closed_by_type IS '执行关闭的发起者类型，未关闭时为空';
COMMENT ON COLUMN conversation_rounds.closed_by_id IS '执行关闭的发起者编号，未关闭时为空';
COMMENT ON COLUMN conversation_rounds.opened_at IS '本轮打开或重新打开的时间';
COMMENT ON COLUMN conversation_rounds.first_reply_at IS '本轮首次回复的时间，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.closed_at IS '本轮关闭的时间，进行中时为空';
COMMENT ON COLUMN conversation_rounds.rating IS '客户对本轮服务的评分（1 到 5 星），未评价时为空';
COMMENT ON COLUMN conversation_rounds.rating_comment IS '客户评价附的一句意见';
COMMENT ON COLUMN conversation_rounds.rated_at IS '客户评价的时间';

-- +goose Down
DROP TABLE conversation_rounds;
