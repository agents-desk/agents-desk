-- +goose Up
CREATE TABLE llm_calls (
    id                uuid PRIMARY KEY,
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,
    tenant_id         uuid REFERENCES tenants (id),
    call_type         text NOT NULL,
    operation         text NOT NULL DEFAULT '',
    meta              jsonb NOT NULL DEFAULT '{}',
    endpoint          text NOT NULL,
    model             text NOT NULL,
    request_id        text NOT NULL DEFAULT '',
    request           jsonb NOT NULL,
    response          text NOT NULL DEFAULT '',
    prompt_tokens     integer NOT NULL DEFAULT 0,
    cached_tokens     integer NOT NULL DEFAULT 0,
    completion_tokens integer NOT NULL DEFAULT 0,
    reasoning_tokens  integer NOT NULL DEFAULT 0,
    cost              numeric(12, 6),
    duration_ms       integer NOT NULL,
    error             text NOT NULL DEFAULT '',
    target_type       text NOT NULL DEFAULT '',
    target_id         text NOT NULL DEFAULT ''
);

CREATE INDEX llm_calls_tenant ON llm_calls (tenant_id, created_at);
CREATE INDEX llm_calls_tenant_type ON llm_calls (tenant_id, call_type, created_at DESC);
CREATE INDEX llm_calls_target ON llm_calls (target_type, target_id);

COMMENT ON TABLE llm_calls IS '大语言模型调用日志：记录每次请求的输入输出、用量与耗时';
COMMENT ON COLUMN llm_calls.id IS '主键';
COMMENT ON COLUMN llm_calls.created_at IS '调用发起时间';
COMMENT ON COLUMN llm_calls.updated_at IS '更新时间';
COMMENT ON COLUMN llm_calls.tenant_id IS '所属工作区 ID；平台级调用可为空';
COMMENT ON COLUMN llm_calls.call_type IS '业务调用类型（如 translation）';
COMMENT ON COLUMN llm_calls.operation IS '本次调用的业务场景名，例如 incoming、fill、reply、back_translate';
COMMENT ON COLUMN llm_calls.meta IS '本次调用的结构化业务元数据，例如源语言与目标语言';
COMMENT ON COLUMN llm_calls.endpoint IS '调用的模型接口地址（不含密钥）';
COMMENT ON COLUMN llm_calls.model IS '实际调用的模型名称';
COMMENT ON COLUMN llm_calls.request_id IS '上游网关返回的请求 ID（如 X-Omirouter-Request-Id），用于与网关日志对账';
COMMENT ON COLUMN llm_calls.request IS '发送给模型的请求载荷（JSON 对象）';
COMMENT ON COLUMN llm_calls.response IS '模型返回的文本响应';
COMMENT ON COLUMN llm_calls.prompt_tokens IS '输入 token 数';
COMMENT ON COLUMN llm_calls.cached_tokens IS '缓存命中的输入 token 数';
COMMENT ON COLUMN llm_calls.completion_tokens IS '输出 token 数（含思考 token）';
COMMENT ON COLUMN llm_calls.reasoning_tokens IS '推理思考消耗的 token 数';
COMMENT ON COLUMN llm_calls.cost IS '供应商返回的调用费用（美元）；未提供时为空';
COMMENT ON COLUMN llm_calls.duration_ms IS '调用耗时（毫秒）';
COMMENT ON COLUMN llm_calls.error IS '调用失败时的错误信息';
COMMENT ON COLUMN llm_calls.target_type IS '关联业务对象类型（如 timeline_item）';
COMMENT ON COLUMN llm_calls.target_id IS '关联业务对象 ID';

-- +goose Down
DROP TABLE llm_calls;
