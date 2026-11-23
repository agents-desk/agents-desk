-- +goose Up
-- llm_calls 增加网关请求 ID（X-Omirouter-Request-Id）与缓存命中输入 token 数。
ALTER TABLE llm_calls ADD COLUMN request_id text NOT NULL DEFAULT '';
ALTER TABLE llm_calls ADD COLUMN cached_tokens integer NOT NULL DEFAULT 0;

COMMENT ON COLUMN llm_calls.request_id IS '上游网关返回的请求 ID（如 X-Omirouter-Request-Id），用于与网关日志对账';
COMMENT ON COLUMN llm_calls.cached_tokens IS '缓存命中的输入 token 数';

-- +goose Down
ALTER TABLE llm_calls DROP COLUMN cached_tokens;
ALTER TABLE llm_calls DROP COLUMN request_id;
