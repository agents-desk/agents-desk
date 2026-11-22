-- +goose Up
-- 团队收件箱支持停用：停用后停止接收新会话与自动分配，从收件箱侧栏隐藏，历史会话和报表保留名称。
ALTER TABLE teams ADD COLUMN disabled_at timestamptz;
COMMENT ON COLUMN teams.disabled_at IS '停用时间，为空表示启用';

-- 工作区常规设置新增单窗口渠道的重开窗口（小时），默认 24 小时，0 表示关闭后不再重开。
ALTER TABLE tenants ADD COLUMN reopen_window_hours integer NOT NULL DEFAULT 24 CHECK (reopen_window_hours >= 0);
COMMENT ON COLUMN tenants.reopen_window_hours IS '单窗口渠道会话关闭后的重开窗口（小时），0 表示关闭后直接新建会话';

-- 会话新增本轮打开时间，并把团队外键改为 RESTRICT，防止误删团队导致历史会话与报表丢失归属。
ALTER TABLE conversations ADD COLUMN opened_at timestamptz;
UPDATE conversations SET opened_at = created_at;
ALTER TABLE conversations ALTER COLUMN opened_at SET NOT NULL;
COMMENT ON COLUMN conversations.opened_at IS '当前这一轮（新建或重新打开）的开始时间';

ALTER TABLE conversations DROP CONSTRAINT conversations_team_id_fkey;
ALTER TABLE conversations ADD CONSTRAINT conversations_team_id_fkey FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE RESTRICT;

-- 每次会话关闭记录：按轮次保存从打开/重新打开到关闭的首次回复时间、关闭时间、归属快照与客户评价。
CREATE TABLE conversation_closures (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    round           integer NOT NULL,
    team_id         uuid REFERENCES teams (id) ON DELETE RESTRICT,
    assignee_type   text NOT NULL DEFAULT '',
    assignee_id     text NOT NULL DEFAULT '',
    closed_by_type  text NOT NULL,
    closed_by_id    text NOT NULL DEFAULT '',
    opened_at       timestamptz NOT NULL,
    first_reply_at  timestamptz,
    closed_at       timestamptz NOT NULL,
    rating          smallint,
    rating_comment  text NOT NULL DEFAULT '',
    rated_at        timestamptz,
    UNIQUE (conversation_id, round)
);
CREATE INDEX conversation_closures_tenant_closed ON conversation_closures (tenant_id, closed_at DESC);
CREATE INDEX conversation_closures_tenant_team ON conversation_closures (tenant_id, team_id, closed_at DESC);
CREATE INDEX conversation_closures_tenant_assignee ON conversation_closures (tenant_id, assignee_id, closed_at DESC);

COMMENT ON TABLE conversation_closures IS '会话每次关闭的轮次记录，保存本轮的首次回复时间、解决时间、归属快照和客户评价';
COMMENT ON COLUMN conversation_closures.id IS '关闭记录 ID（UUIDv7）';
COMMENT ON COLUMN conversation_closures.created_at IS '创建时间';
COMMENT ON COLUMN conversation_closures.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_closures.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN conversation_closures.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN conversation_closures.round IS '会话内第几次关闭，从 1 递增';
COMMENT ON COLUMN conversation_closures.team_id IS '关闭时归属的团队收件箱 ID';
COMMENT ON COLUMN conversation_closures.assignee_type IS '关闭时的负责人类型，为空表示无负责人';
COMMENT ON COLUMN conversation_closures.assignee_id IS '关闭时的负责人编号';
COMMENT ON COLUMN conversation_closures.closed_by_type IS '执行关闭的发起者类型';
COMMENT ON COLUMN conversation_closures.closed_by_id IS '执行关闭的发起者编号';
COMMENT ON COLUMN conversation_closures.opened_at IS '本轮打开（或重新打开）的时间';
COMMENT ON COLUMN conversation_closures.first_reply_at IS '本轮首次回复的时间，未回复即关闭时为空';
COMMENT ON COLUMN conversation_closures.closed_at IS '本轮关闭的时间';
COMMENT ON COLUMN conversation_closures.rating IS '客户对本轮关闭的评分（1 到 5 星），未评价时为空';
COMMENT ON COLUMN conversation_closures.rating_comment IS '客户评价附的一句意见';
COMMENT ON COLUMN conversation_closures.rated_at IS '客户评价的时间';

-- +goose Down
DROP TABLE conversation_closures;
ALTER TABLE conversations DROP CONSTRAINT conversations_team_id_fkey;
ALTER TABLE conversations ADD CONSTRAINT conversations_team_id_fkey FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE SET NULL;
ALTER TABLE conversations DROP COLUMN opened_at;
ALTER TABLE tenants DROP COLUMN reopen_window_hours;
ALTER TABLE teams DROP COLUMN disabled_at;
