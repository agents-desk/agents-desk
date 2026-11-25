-- +goose Up
CREATE TABLE timeline_attachments (
    id                uuid PRIMARY KEY,
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,
    tenant_id         uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    item_id           uuid NOT NULL REFERENCES timeline_items (id) ON DELETE CASCADE,
    position          integer NOT NULL CHECK (position >= 0),
    kind              text NOT NULL,
    filename          text NOT NULL DEFAULT '',
    content_type      text NOT NULL DEFAULT '',
    size              bigint NOT NULL DEFAULT 0 CHECK (size >= 0),
    width             integer NOT NULL DEFAULT 0 CHECK (width >= 0),
    height            integer NOT NULL DEFAULT 0 CHECK (height >= 0),
    duration_ms       integer NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    is_voice          boolean NOT NULL DEFAULT false,
    file_id           uuid REFERENCES files (id),
    thumb_file_id     uuid REFERENCES files (id),
    source_channel_id uuid REFERENCES channels (id) ON DELETE SET NULL,
    source_ref        text NOT NULL DEFAULT '',
    thumb_source_ref  text NOT NULL DEFAULT '',
    fetch_status      text NOT NULL DEFAULT 'ready',
    fetch_error       text NOT NULL DEFAULT '',
    CONSTRAINT timeline_attachments_item_pos UNIQUE (item_id, position),
    CONSTRAINT timeline_attachments_file UNIQUE (file_id),
    CONSTRAINT timeline_attachments_thumb UNIQUE (thumb_file_id)
);

CREATE INDEX timeline_attachments_item_idx ON timeline_attachments (item_id, position);
CREATE INDEX timeline_attachments_tenant_status_idx ON timeline_attachments (tenant_id, fetch_status) WHERE fetch_status <> 'ready';

COMMENT ON TABLE timeline_attachments IS '时间线消息和内部备注附带的媒体附件（图片、视频、音频/语音、文件），支持已落盘与外部渠道按需拉取';
COMMENT ON COLUMN timeline_attachments.id IS '附件唯一标识（UUIDv7）';
COMMENT ON COLUMN timeline_attachments.created_at IS '创建时间';
COMMENT ON COLUMN timeline_attachments.updated_at IS '最后更新时间';
COMMENT ON COLUMN timeline_attachments.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN timeline_attachments.item_id IS '所属时间线项的 ID';
COMMENT ON COLUMN timeline_attachments.position IS '附件在这条消息中的顺序，从 0 开始';
COMMENT ON COLUMN timeline_attachments.kind IS '附件展示类型：image、video、audio、file';
COMMENT ON COLUMN timeline_attachments.filename IS '原始文件名';
COMMENT ON COLUMN timeline_attachments.content_type IS '规范 MIME 类型';
COMMENT ON COLUMN timeline_attachments.size IS '主文件大小（字节）';
COMMENT ON COLUMN timeline_attachments.width IS '图片或视频宽度（像素）';
COMMENT ON COLUMN timeline_attachments.height IS '图片或视频高度（像素）';
COMMENT ON COLUMN timeline_attachments.duration_ms IS '音频或视频时长（毫秒）';
COMMENT ON COLUMN timeline_attachments.is_voice IS '是否为语音条消息（用于与普通音频文件区分渲染）';
COMMENT ON COLUMN timeline_attachments.file_id IS '主文件在 files 表中的已确认文件 ID；远端尚未拉取时为空';
COMMENT ON COLUMN timeline_attachments.thumb_file_id IS '封面缩略图在 files 表中的已确认文件 ID；无独立封面时为空';
COMMENT ON COLUMN timeline_attachments.source_channel_id IS '外部渠道来源 ID；网页端直传附件为空';
COMMENT ON COLUMN timeline_attachments.source_ref IS '外部渠道主文件的不透明引用标识（例如 Telegram file_id）';
COMMENT ON COLUMN timeline_attachments.thumb_source_ref IS '外部渠道封面缩略图的不透明引用标识（例如 Telegram thumbnail.file_id）';
COMMENT ON COLUMN timeline_attachments.fetch_status IS '主文件物化状态：ready、remote、pending、fetching、unavailable';
COMMENT ON COLUMN timeline_attachments.fetch_error IS '不可用或最近一次拉取失败的错误码或说明';

-- +goose Down
DROP TABLE timeline_attachments;
