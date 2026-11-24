-- +goose Up
CREATE TABLE automation_rules (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    builtin_key text NOT NULL DEFAULT '',
    name        text NOT NULL DEFAULT '',
    trigger     text NOT NULL,
    conditions  jsonb NOT NULL DEFAULT '{}',
    actions     jsonb NOT NULL DEFAULT '[]',
    enabled     boolean NOT NULL DEFAULT true,
    position    integer NOT NULL DEFAULT 0
);

CREATE INDEX automation_rules_tenant_trigger_idx
    ON automation_rules (tenant_id, trigger, position) WHERE enabled;
CREATE UNIQUE INDEX automation_rules_tenant_builtin_idx
    ON automation_rules (tenant_id, builtin_key) WHERE builtin_key <> '';

COMMENT ON TABLE automation_rules IS '自动化规则：定义触发器、条件和动作';
COMMENT ON COLUMN automation_rules.id IS '主键';
COMMENT ON COLUMN automation_rules.created_at IS '创建时间';
COMMENT ON COLUMN automation_rules.updated_at IS '更新时间';
COMMENT ON COLUMN automation_rules.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN automation_rules.builtin_key IS '内置规则标识；自定义规则为空';
COMMENT ON COLUMN automation_rules.name IS '规则名称';
COMMENT ON COLUMN automation_rules.trigger IS '触发事件类型';
COMMENT ON COLUMN automation_rules.conditions IS '规则匹配条件定义（JSON 对象）';
COMMENT ON COLUMN automation_rules.actions IS '匹配后执行的动作列表（JSON 数组）';
COMMENT ON COLUMN automation_rules.enabled IS '规则是否启用';
COMMENT ON COLUMN automation_rules.position IS '同一触发器下的执行排序序号';

-- +goose Down
DROP TABLE automation_rules;
