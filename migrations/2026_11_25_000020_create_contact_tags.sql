-- +goose Up
CREATE TABLE contact_tags (
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    contact_id uuid NOT NULL REFERENCES contacts (id) ON DELETE CASCADE,
    tag_id     uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (contact_id, tag_id)
);

CREATE INDEX contact_tags_tenant_tag ON contact_tags (tenant_id, tag_id, contact_id);

COMMENT ON TABLE contact_tags IS '联系人与标签的多对多关联';
COMMENT ON COLUMN contact_tags.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN contact_tags.contact_id IS '联系人 ID';
COMMENT ON COLUMN contact_tags.tag_id IS '标签 ID';
COMMENT ON COLUMN contact_tags.created_at IS '打标时间';

-- +goose Down
DROP TABLE contact_tags;
