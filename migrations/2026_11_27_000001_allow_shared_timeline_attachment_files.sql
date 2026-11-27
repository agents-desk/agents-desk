-- +goose Up
ALTER TABLE timeline_attachments DROP CONSTRAINT timeline_attachments_file;
ALTER TABLE timeline_attachments DROP CONSTRAINT timeline_attachments_thumb;
ALTER TABLE timeline_attachments ADD CONSTRAINT timeline_attachments_file UNIQUE (item_id, file_id);
ALTER TABLE timeline_attachments ADD CONSTRAINT timeline_attachments_thumb UNIQUE (item_id, thumb_file_id);

-- +goose Down
ALTER TABLE timeline_attachments DROP CONSTRAINT timeline_attachments_file;
ALTER TABLE timeline_attachments DROP CONSTRAINT timeline_attachments_thumb;
ALTER TABLE timeline_attachments ADD CONSTRAINT timeline_attachments_file UNIQUE (file_id);
ALTER TABLE timeline_attachments ADD CONSTRAINT timeline_attachments_thumb UNIQUE (thumb_file_id);
