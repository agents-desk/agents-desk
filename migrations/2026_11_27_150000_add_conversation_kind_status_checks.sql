-- +goose Up
-- conversations.status 在各会话种类下使用不同的取值域：服务型是 open/snoozed/closed，工单是四个状态大类，
-- 聊天型不推进状态（固定 open）。在数据库层把取值域约束住，新增统计、导出或工具查询漏写 kind 条件时，
-- 不同种类的状态不会悄悄混在一起。

ALTER TABLE conversations
    ADD CONSTRAINT conversations_service_status
        CHECK (kind <> 'service' OR status IN ('open', 'snoozed', 'closed')),
    ADD CONSTRAINT conversations_ticket_status
        CHECK (kind <> 'ticket' OR status IN ('submitted', 'in_progress', 'waiting_on_customer', 'resolved')),
    ADD CONSTRAINT conversations_chat_status
        CHECK (kind <> 'chat' OR status = 'open');

COMMENT ON COLUMN conversations.status IS '会话状态，取值域由 kind 决定：服务型为 open/snoozed/closed，工单为 submitted/in_progress/waiting_on_customer/resolved，聊天型固定 open';

-- +goose Down
COMMENT ON COLUMN conversations.status IS '会话状态：open（进行中）、snoozed（延后）或 closed（已关闭）';

ALTER TABLE conversations
    DROP CONSTRAINT IF EXISTS conversations_chat_status,
    DROP CONSTRAINT IF EXISTS conversations_ticket_status,
    DROP CONSTRAINT IF EXISTS conversations_service_status;
