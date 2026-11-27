-- +goose Up
-- 为高频查询、并发锁、会话合并、后台导出、媒体验证与清理任务补充复合索引及部分索引。

CREATE INDEX IF NOT EXISTS conversation_participants_participant_active
    ON conversation_participants (participant_type, participant_id, conversation_id)
    WHERE left_at IS NULL;

CREATE INDEX IF NOT EXISTS conversation_participants_participant_conv
    ON conversation_participants (participant_type, participant_id, conversation_id);

CREATE INDEX IF NOT EXISTS conversations_tenant_kind_status_last_msg
    ON conversations (tenant_id, kind, status, last_message_at DESC);

CREATE INDEX IF NOT EXISTS conversations_snoozed_until
    ON conversations (snoozed_until)
    WHERE status = 'snoozed' AND snoozed_until IS NOT NULL;

CREATE INDEX IF NOT EXISTS conversation_mentions_member_created
    ON conversation_mentions (tenant_id, member_id, created_at DESC);

CREATE INDEX IF NOT EXISTS conversation_mentions_conv
    ON conversation_mentions (conversation_id);

CREATE INDEX IF NOT EXISTS conversation_reads_conv
    ON conversation_reads (conversation_id);

CREATE INDEX IF NOT EXISTS message_deliveries_external_id
    ON message_deliveries (external_id)
    WHERE external_id <> '';

CREATE INDEX IF NOT EXISTS message_deliveries_status
    ON message_deliveries (status, updated_at)
    WHERE status IN ('sending', 'failed');

CREATE INDEX IF NOT EXISTS timeline_attachments_file_idx
    ON timeline_attachments (file_id)
    WHERE file_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS timeline_attachments_thumb_file_idx
    ON timeline_attachments (thumb_file_id)
    WHERE thumb_file_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS llm_calls_created_at
    ON llm_calls (created_at);

-- +goose Down
DROP INDEX IF EXISTS llm_calls_created_at;
DROP INDEX IF EXISTS timeline_attachments_thumb_file_idx;
DROP INDEX IF EXISTS timeline_attachments_file_idx;
DROP INDEX IF EXISTS message_deliveries_status;
DROP INDEX IF EXISTS message_deliveries_external_id;
DROP INDEX IF EXISTS conversation_reads_conv;
DROP INDEX IF EXISTS conversation_mentions_conv;
DROP INDEX IF EXISTS conversation_mentions_member_created;
DROP INDEX IF EXISTS conversations_snoozed_until;
DROP INDEX IF EXISTS conversations_tenant_kind_status_last_msg;
DROP INDEX IF EXISTS conversation_participants_participant_conv;
DROP INDEX IF EXISTS conversation_participants_participant_active;
