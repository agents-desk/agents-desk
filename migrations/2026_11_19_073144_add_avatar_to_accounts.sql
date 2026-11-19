-- +goose Up
-- 头像是 files 表中的一个文件，没有头像时为空。换头像时旧文件由 filesystem 标记删除、定时清理。
ALTER TABLE platform_admins ADD COLUMN avatar_file_id uuid REFERENCES files (id);
ALTER TABLE members ADD COLUMN avatar_file_id uuid REFERENCES files (id);

-- +goose Down
ALTER TABLE members DROP COLUMN avatar_file_id;
ALTER TABLE platform_admins DROP COLUMN avatar_file_id;
