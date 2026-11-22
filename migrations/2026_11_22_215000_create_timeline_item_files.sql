-- +goose Up
CREATE TABLE timeline_item_files (
    item_id  uuid NOT NULL REFERENCES timeline_items (id) ON DELETE CASCADE,
    file_id  uuid NOT NULL REFERENCES files (id),
    position int  NOT NULL CHECK (position >= 0),
    PRIMARY KEY (item_id, position),
    CONSTRAINT timeline_item_files_file UNIQUE (file_id)
);
COMMENT ON TABLE timeline_item_files IS '时间线消息和内部备注附带的图片和文件';
COMMENT ON COLUMN timeline_item_files.item_id IS '所属时间线项的 ID';
COMMENT ON COLUMN timeline_item_files.file_id IS '关联的已确认文件 ID（files.id）';
COMMENT ON COLUMN timeline_item_files.position IS '附件在这条消息中的顺序，从 0 开始';

-- +goose Down
DROP TABLE timeline_item_files;
