-- +goose Up
ALTER TABLE tenants ADD COLUMN logo_file_id uuid REFERENCES files (id);

COMMENT ON COLUMN tenants.logo_file_id IS '工作区 Logo 文件 ID，关联 files.id；为空表示未设置';

-- +goose Down
ALTER TABLE tenants DROP COLUMN logo_file_id;
