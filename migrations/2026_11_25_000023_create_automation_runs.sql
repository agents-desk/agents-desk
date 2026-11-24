-- +goose Up
CREATE TABLE automation_runs (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    rule_id         uuid NOT NULL REFERENCES automation_rules (id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    trigger_item_id uuid NOT NULL,
    dedup_key       text NOT NULL,
    status          text NOT NULL,
    error           text NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX automation_runs_rule_dedup_idx
    ON automation_runs (rule_id, dedup_key);
CREATE INDEX automation_runs_tenant_conv_idx
    ON automation_runs (tenant_id, conversation_id, created_at DESC);

COMMENT ON TABLE automation_runs IS '自动化规则执行记录与幂等去重日志';
COMMENT ON COLUMN automation_runs.id IS '主键';
COMMENT ON COLUMN automation_runs.created_at IS '执行时间';
COMMENT ON COLUMN automation_runs.updated_at IS '更新时间';
COMMENT ON COLUMN automation_runs.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN automation_runs.rule_id IS '执行的自动化规则 ID';
COMMENT ON COLUMN automation_runs.conversation_id IS '目标会话 ID';
COMMENT ON COLUMN automation_runs.trigger_item_id IS '触发本次执行的时间线条目 ID';
COMMENT ON COLUMN automation_runs.dedup_key IS '幂等去重键，同一规则下唯一';
COMMENT ON COLUMN automation_runs.status IS '执行状态（如 succeeded、failed、skipped）';
COMMENT ON COLUMN automation_runs.error IS '执行失败时的错误信息';

-- +goose Down
DROP TABLE automation_runs;
