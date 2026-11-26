-- +goose Up
-- 收敛历史上同一联系人并存的多条未关闭服务型会话：保留最近活跃的一条，其余旧活跃会话标记为已关闭并收口对应轮次。
WITH ranked AS (
    SELECT
        id,
        tenant_id,
        last_message_at,
        ROW_NUMBER() OVER (
            PARTITION BY tenant_id, contact_id
            ORDER BY last_message_at DESC, id DESC
        ) AS rn
    FROM conversations
    WHERE kind = 'service'
      AND contact_id IS NOT NULL
      AND status IN ('open', 'snoozed')
),
to_close AS (
    SELECT id, tenant_id, last_message_at
    FROM ranked
    WHERE rn > 1
)
UPDATE conversations c
SET
    status = 'closed',
    closed_at = COALESCE(c.closed_at, tc.last_message_at),
    snoozed_until = NULL,
    waiting_since = NULL
FROM to_close tc
WHERE c.id = tc.id
  AND c.tenant_id = tc.tenant_id;

UPDATE conversation_rounds r
SET
    closed_at = COALESCE(r.closed_at, c.closed_at, c.last_message_at)
FROM conversations c
WHERE r.conversation_id = c.id
  AND r.tenant_id = c.tenant_id
  AND c.kind = 'service'
  AND c.status = 'closed'
  AND r.closed_at IS NULL;

CREATE UNIQUE INDEX conversations_one_active_service_per_contact
    ON conversations (tenant_id, contact_id)
    WHERE kind = 'service' AND contact_id IS NOT NULL AND status IN ('open', 'snoozed');

-- 移除网页聊天插件渠道设置中已废弃的 behavior（conversation_mode）配置项。
UPDATE channels
SET settings = settings - 'behavior'
WHERE kind = 'widget'
  AND settings ? 'behavior';

-- +goose Down
DROP INDEX IF EXISTS conversations_one_active_service_per_contact;
