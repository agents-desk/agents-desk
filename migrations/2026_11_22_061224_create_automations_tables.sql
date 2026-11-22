-- +goose Up
-- 自动化规则与执行记录：第一版内置三条规则（管理员可以开关），内部按触发 → 条件 → 动作执行。
CREATE TABLE automation_rules (
    id          uuid        PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    builtin_key text        NOT NULL DEFAULT '',
    name        text        NOT NULL DEFAULT '',
    trigger     text        NOT NULL,
    conditions  jsonb       NOT NULL DEFAULT '{}',
    actions     jsonb       NOT NULL DEFAULT '[]',
    enabled     boolean     NOT NULL DEFAULT true,
    position    integer     NOT NULL DEFAULT 0
);

CREATE INDEX automation_rules_tenant_trigger_idx
    ON automation_rules (tenant_id, trigger, position) WHERE enabled;

CREATE UNIQUE INDEX automation_rules_tenant_builtin_idx
    ON automation_rules (tenant_id, builtin_key) WHERE builtin_key <> '';

-- 自动化执行记录：按 (rule_id, dedup_key) 去重，保证重复投递和多次重试不会重复产生副作用。
CREATE TABLE automation_runs (
    id              uuid        PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_id         uuid        NOT NULL REFERENCES automation_rules(id) ON DELETE CASCADE,
    conversation_id uuid        NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    trigger_item_id uuid        NOT NULL,
    dedup_key       text        NOT NULL,
    status          text        NOT NULL,
    error           text        NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX automation_runs_rule_dedup_idx
    ON automation_runs (rule_id, dedup_key);

CREATE INDEX automation_runs_tenant_conv_idx
    ON automation_runs (tenant_id, conversation_id, created_at DESC);

-- +goose Down
DROP TABLE automation_runs;
DROP TABLE automation_rules;
