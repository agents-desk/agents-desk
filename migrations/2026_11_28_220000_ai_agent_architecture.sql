-- +goose Up
-- AI Agent 统一架构与会话控制权迁移：
-- 1. conversations 增加显式接待控制权 handling_mode；
-- 2. timeline_items 增加逻辑排序锚点 after_seq 与生成归因 generation_id；
-- 3. generations 支持草稿模式、单活索引、取消原因、工具审批与检索引用；
-- 4. 新建 agent_turns 表保存 AI 中间工具调用轮次；
-- 5. conversation_rounds 增加 AI 解决归因与转人工原因字段。

ALTER TABLE conversations
    ADD COLUMN handling_mode text NOT NULL DEFAULT 'human'
        CHECK (handling_mode IN ('human', 'ai_auto', 'ai_copilot', 'ai_paused', 'waiting_approval'));

CREATE INDEX conversations_tenant_handling ON conversations (tenant_id, handling_mode)
    WHERE status <> 'closed';

COMMENT ON COLUMN conversations.handling_mode IS '接待控制模式：human 人工、ai_auto AI 自动接待、ai_copilot AI 副驾驶、ai_paused 人工接管暂停 AI、waiting_approval 等待工具审批';

ALTER TABLE timeline_items
    ADD COLUMN after_seq bigint,
    ADD COLUMN generation_id uuid REFERENCES generations (id) ON DELETE SET NULL;

CREATE INDEX timeline_items_generation ON timeline_items (generation_id)
    WHERE generation_id IS NOT NULL;

COMMENT ON COLUMN timeline_items.after_seq IS '逻辑上下文锚点序号：AI 运行基于该序号之前的历史生成回复';
COMMENT ON COLUMN timeline_items.generation_id IS '关联或采纳的 AI 生成 ID（Reply 直接定稿或 Copilot 草稿采纳）';

ALTER TABLE generations
    DROP CONSTRAINT IF EXISTS generations_check;

ALTER TABLE generations
    ADD COLUMN mode text NOT NULL DEFAULT 'reply' CHECK (mode IN ('reply', 'draft')),
    ADD COLUMN run_id text NOT NULL DEFAULT '',
    ADD COLUMN trigger_seq bigint NOT NULL DEFAULT 0,
    ADD COLUMN cancel_reason text NOT NULL DEFAULT '',
    ADD COLUMN pending_approvals jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN tool_calls jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN citations jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN prompt_version text NOT NULL DEFAULT '';

ALTER TABLE generations
    ADD CONSTRAINT generations_completed_item_check
        CHECK (state <> 'completed' OR mode = 'draft' OR item_id IS NOT NULL);

CREATE UNIQUE INDEX generations_one_active_per_conversation ON generations (conversation_id)
    WHERE state IN ('pending', 'streaming', 'waiting_approval');

COMMENT ON COLUMN generations.mode IS '生成模式：reply 直接回复并入时间线，draft 副驾驶草稿（不直接发送）';
COMMENT ON COLUMN generations.run_id IS '底层 ai/chat.Run 的运行标识，用于跨实例续传、插话和取消';
COMMENT ON COLUMN generations.trigger_seq IS '触发这次生成的消息时间线序号';
COMMENT ON COLUMN generations.cancel_reason IS '取消原因：manual、human_takeover、barge_in、conversation_closed 或 merged';
COMMENT ON COLUMN generations.pending_approvals IS '等待成员确认的工具调用列表（JSON 数组）';
COMMENT ON COLUMN generations.tool_calls IS '本次生成执行的工具调用与耗时记录（JSON 数组）';
COMMENT ON COLUMN generations.citations IS '本次生成引用的知识库切片证据列表（JSON 数组）';
COMMENT ON COLUMN generations.prompt_version IS '触发本次生成时所使用的提示词版本标识';

CREATE TABLE agent_turns (
    id              uuid PRIMARY KEY,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    generation_id   uuid REFERENCES generations (id) ON DELETE SET NULL,
    run_id          text NOT NULL DEFAULT '',
    after_seq       bigint NOT NULL DEFAULT 0,
    turn_order      integer NOT NULL DEFAULT 0,
    role            text NOT NULL CHECK (role IN ('assistant', 'tool')),
    message         jsonb NOT NULL
);

CREATE INDEX agent_turns_conv_order ON agent_turns (tenant_id, conversation_id, after_seq, turn_order, created_at);

COMMENT ON TABLE agent_turns IS 'AI 中间轮次：保存带工具调用的 assistant 与 tool 消息，供 ai/chat 断点审批与恢复上下文';
COMMENT ON COLUMN agent_turns.id IS '主键';
COMMENT ON COLUMN agent_turns.created_at IS '创建时间';
COMMENT ON COLUMN agent_turns.updated_at IS '更新时间';
COMMENT ON COLUMN agent_turns.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN agent_turns.conversation_id IS '所属会话 ID';
COMMENT ON COLUMN agent_turns.generation_id IS '产生该轮次的生成 ID';
COMMENT ON COLUMN agent_turns.run_id IS '运行 ID';
COMMENT ON COLUMN agent_turns.after_seq IS '基准时间线锚点序号';
COMMENT ON COLUMN agent_turns.turn_order IS '同一锚点序号下的轮次递增顺序';
COMMENT ON COLUMN agent_turns.role IS '消息角色：assistant（带工具调用）或 tool（工具结果）';
COMMENT ON COLUMN agent_turns.message IS '序列化后的 model.Message 内容（JSON 对象）';

ALTER TABLE conversation_rounds
    ADD COLUMN resolution_type text NOT NULL DEFAULT ''
        CHECK (resolution_type IN ('', 'ai_resolved', 'ai_assisted', 'human_resolved')),
    ADD COLUMN handover_reason text NOT NULL DEFAULT '',
    ADD COLUMN ai_generation_count integer NOT NULL DEFAULT 0;

CREATE INDEX conversation_rounds_tenant_resolution
    ON conversation_rounds (tenant_id, resolution_type, closed_at DESC)
    WHERE closed_at IS NOT NULL AND resolution_type <> '';

COMMENT ON COLUMN conversation_rounds.resolution_type IS '本轮解决归因：ai_resolved（AI 独立解决）、ai_assisted（AI 辅助人工）或 human_resolved（纯人工解决）';
COMMENT ON COLUMN conversation_rounds.handover_reason IS '最近一次从 AI 转交人工的原因标识';
COMMENT ON COLUMN conversation_rounds.ai_generation_count IS '本轮服务期间产生的 AI 生成次数';

-- +goose Down
DROP INDEX IF EXISTS conversation_rounds_tenant_resolution;
ALTER TABLE conversation_rounds
    DROP COLUMN IF EXISTS ai_generation_count,
    DROP COLUMN IF EXISTS handover_reason,
    DROP COLUMN IF EXISTS resolution_type;

DROP INDEX IF EXISTS agent_turns_conv_order;
DROP TABLE IF EXISTS agent_turns;

DROP INDEX IF EXISTS generations_one_active_per_conversation;
ALTER TABLE generations
    DROP CONSTRAINT IF EXISTS generations_completed_item_check,
    DROP COLUMN IF EXISTS prompt_version,
    DROP COLUMN IF EXISTS citations,
    DROP COLUMN IF EXISTS tool_calls,
    DROP COLUMN IF EXISTS pending_approvals,
    DROP COLUMN IF EXISTS cancel_reason,
    DROP COLUMN IF EXISTS trigger_seq,
    DROP COLUMN IF EXISTS run_id,
    DROP COLUMN IF EXISTS mode;

ALTER TABLE generations
    ADD CONSTRAINT generations_check CHECK (state <> 'completed' OR item_id IS NOT NULL);

DROP INDEX IF EXISTS timeline_items_generation;
ALTER TABLE timeline_items
    DROP COLUMN IF EXISTS generation_id,
    DROP COLUMN IF EXISTS after_seq;

DROP INDEX IF EXISTS conversations_tenant_handling;
ALTER TABLE conversations
    DROP COLUMN IF EXISTS handling_mode;
