-- +goose Up
-- 1. 将 conversation_closures 升级为全生命周期的服务轮次表 conversation_rounds：
--    会话新建和重新打开时即写入一轮，首次回复时记录首次回复人与时间，关闭时补全关闭时间与归属快照，评价时记录客户评分。
ALTER TABLE conversation_closures RENAME TO conversation_rounds;

ALTER INDEX conversation_closures_pkey RENAME TO conversation_rounds_pkey;
ALTER INDEX conversation_closures_conversation_id_round_key RENAME TO conversation_rounds_conversation_id_round_key;
DROP INDEX IF EXISTS conversation_closures_tenant_closed;
DROP INDEX IF EXISTS conversation_closures_tenant_team;
DROP INDEX IF EXISTS conversation_closures_tenant_assignee;

ALTER TABLE conversation_rounds ALTER COLUMN closed_by_type SET DEFAULT '';
ALTER TABLE conversation_rounds ALTER COLUMN closed_at DROP NOT NULL;
ALTER TABLE conversation_rounds ADD COLUMN first_reply_by_type text NOT NULL DEFAULT '';
ALTER TABLE conversation_rounds ADD COLUMN first_reply_by_id text NOT NULL DEFAULT '';

-- 为已存在但尚未关闭的服务型会话补齐当前进行中的轮次行（仅使用既有业务时间，不使用数据库时钟函数）。
INSERT INTO conversation_rounds (
    id, created_at, updated_at, tenant_id, conversation_id, round,
    team_id, assignee_type, assignee_id, closed_by_type, closed_by_id,
    opened_at, first_reply_at, closed_at, first_reply_by_type, first_reply_by_id
)
SELECT
    c.id, c.opened_at, c.updated_at, c.tenant_id, c.id,
    COALESCE((SELECT max(r.round) FROM conversation_rounds r WHERE r.conversation_id = c.id), 0) + 1,
    c.team_id, c.assignee_type, c.assignee_id, '', '',
    c.opened_at, c.first_reply_at, NULL,
    CASE WHEN c.first_reply_at IS NOT NULL THEN c.assignee_type ELSE '' END,
    CASE WHEN c.first_reply_at IS NOT NULL THEN c.assignee_id ELSE '' END
FROM conversations c
WHERE c.kind = 'service'
  AND c.status <> 'closed'
  AND NOT EXISTS (
      SELECT 1 FROM conversation_rounds r WHERE r.conversation_id = c.id AND r.closed_at IS NULL
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
COMMENT ON COLUMN conversation_rounds.round IS '会话内第几轮服务，从 1 递增';
COMMENT ON COLUMN conversation_rounds.team_id IS '本轮归属的团队收件箱 ID（关闭后保留快照）';
COMMENT ON COLUMN conversation_rounds.assignee_type IS '本轮负责人类型，为空表示无负责人';
COMMENT ON COLUMN conversation_rounds.assignee_id IS '本轮负责人编号';
COMMENT ON COLUMN conversation_rounds.closed_by_type IS '执行关闭的发起者类型，未关闭时为空';
COMMENT ON COLUMN conversation_rounds.closed_by_id IS '执行关闭的发起者编号，未关闭时为空';
COMMENT ON COLUMN conversation_rounds.opened_at IS '本轮打开或重新打开的时间';
COMMENT ON COLUMN conversation_rounds.first_reply_at IS '本轮首次回复的时间，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.first_reply_by_type IS '本轮首次回复的发起者类型，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.first_reply_by_id IS '本轮首次回复的发起者编号，尚未回复时为空';
COMMENT ON COLUMN conversation_rounds.closed_at IS '本轮关闭的时间，进行中时为空';
COMMENT ON COLUMN conversation_rounds.rating IS '客户对本轮服务的评分（1 到 5 星），未评价时为空';

-- 2. llm_calls 增加一等字段 operation 与 meta，避免报表解析请求 JSON 内部结构。
ALTER TABLE llm_calls ADD COLUMN operation text NOT NULL DEFAULT '';
ALTER TABLE llm_calls ADD COLUMN meta jsonb NOT NULL DEFAULT '{}';
CREATE INDEX llm_calls_tenant_type ON llm_calls (tenant_id, call_type, created_at DESC);

COMMENT ON COLUMN llm_calls.operation IS '本次调用的业务场景名，例如 incoming、fill、reply、back_translate';
COMMENT ON COLUMN llm_calls.meta IS '本次调用的结构化业务元数据，例如源语言与目标语言';

-- 3. 补充报表查询索引。
CREATE INDEX conversations_tenant_kind_created ON conversations (tenant_id, kind, created_at DESC);
CREATE INDEX timeline_items_tenant_message_created ON timeline_items (tenant_id, created_at DESC) WHERE kind = 'message';

-- +goose Down
DROP INDEX IF EXISTS timeline_items_tenant_message_created;
DROP INDEX IF EXISTS conversations_tenant_kind_created;

DROP INDEX IF EXISTS llm_calls_tenant_type;
ALTER TABLE llm_calls DROP COLUMN meta;
ALTER TABLE llm_calls DROP COLUMN operation;

DELETE FROM conversation_rounds WHERE closed_at IS NULL;
DROP INDEX IF EXISTS conversation_rounds_tenant_first_replier;
DROP INDEX IF EXISTS conversation_rounds_tenant_assignee;
DROP INDEX IF EXISTS conversation_rounds_tenant_team;
DROP INDEX IF EXISTS conversation_rounds_tenant_rated;
DROP INDEX IF EXISTS conversation_rounds_tenant_closed;
DROP INDEX IF EXISTS conversation_rounds_tenant_first_reply;
DROP INDEX IF EXISTS conversation_rounds_tenant_opened;

ALTER TABLE conversation_rounds DROP COLUMN first_reply_by_id;
ALTER TABLE conversation_rounds DROP COLUMN first_reply_by_type;
ALTER TABLE conversation_rounds ALTER COLUMN closed_at SET NOT NULL;
ALTER TABLE conversation_rounds ALTER COLUMN closed_by_type DROP DEFAULT;
ALTER INDEX conversation_rounds_conversation_id_round_key RENAME TO conversation_closures_conversation_id_round_key;
ALTER INDEX conversation_rounds_pkey RENAME TO conversation_closures_pkey;
ALTER TABLE conversation_rounds RENAME TO conversation_closures;

CREATE INDEX conversation_closures_tenant_closed ON conversation_closures (tenant_id, closed_at DESC);
CREATE INDEX conversation_closures_tenant_team ON conversation_closures (tenant_id, team_id, closed_at DESC);
CREATE INDEX conversation_closures_tenant_assignee ON conversation_closures (tenant_id, assignee_id, closed_at DESC);
