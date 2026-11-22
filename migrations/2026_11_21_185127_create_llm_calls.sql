-- +goose Up
-- 模型调用记录：每次调用一行，失败的也记。以后用来回放、评测和统计用量、费用。
-- call_type 是调用类型（translation 等），endpoint 是接口的地址（不含密钥），request、response 是发出的请求和收到的回答，
-- completion_tokens 包括思考用的 reasoning_tokens，cost 是供应商返回的费用（美元，没有时为空），target_type、target_id 是这次调用为谁做的，例如 timeline_item 和它的 ID。
CREATE TABLE llm_calls (
    id                uuid PRIMARY KEY,
    created_at        timestamptz NOT NULL,
    tenant_id         uuid REFERENCES tenants (id),
    call_type         text NOT NULL,
    endpoint          text NOT NULL,
    model             text NOT NULL,
    request           jsonb NOT NULL,
    response          text NOT NULL DEFAULT '',
    prompt_tokens     integer NOT NULL DEFAULT 0,
    completion_tokens integer NOT NULL DEFAULT 0,
    reasoning_tokens  integer NOT NULL DEFAULT 0,
    cost              numeric(12, 6),
    duration_ms       integer NOT NULL,
    error             text NOT NULL DEFAULT '',
    target_type       text NOT NULL DEFAULT '',
    target_id         text NOT NULL DEFAULT ''
);
CREATE INDEX llm_calls_tenant ON llm_calls (tenant_id, created_at);
CREATE INDEX llm_calls_target ON llm_calls (target_type, target_id);

-- +goose Down
DROP TABLE llm_calls;
