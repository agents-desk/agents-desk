-- +goose Up
CREATE TABLE contact_identities (
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    channel_id  uuid NOT NULL REFERENCES channels (id),
    external_id text NOT NULL,
    contact_id  uuid NOT NULL REFERENCES contacts (id),
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (channel_id, external_id)
);

CREATE INDEX contact_identities_contact ON contact_identities (contact_id);

COMMENT ON TABLE contact_identities IS '联系人在各接入渠道中的外部身份映射';
COMMENT ON COLUMN contact_identities.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN contact_identities.channel_id IS '渠道 ID';
COMMENT ON COLUMN contact_identities.external_id IS '联系人在该渠道中的外部标识';
COMMENT ON COLUMN contact_identities.contact_id IS '关联的联系人 ID';
COMMENT ON COLUMN contact_identities.created_at IS '创建时间';

-- +goose Down
DROP TABLE contact_identities;
