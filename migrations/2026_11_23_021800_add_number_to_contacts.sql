-- +goose Up
-- 联系人在工作区内的递增编号：用于匿名访客展示（如“访客 #128”）与按编号搜索，不污染真实姓名。
ALTER TABLE contacts
    ADD COLUMN number bigint;

WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY tenant_id ORDER BY created_at ASC, id ASC) AS rn
    FROM contacts
)
UPDATE contacts c
SET number = ranked.rn
FROM ranked
WHERE c.id = ranked.id;

ALTER TABLE contacts
    ALTER COLUMN number SET NOT NULL,
    ADD CONSTRAINT contacts_number_positive CHECK (number > 0);

CREATE UNIQUE INDEX contacts_tenant_number ON contacts (tenant_id, number);
COMMENT ON COLUMN contacts.number IS '联系人在工作区内的递增编号，从 1 开始，创建后不变';

ALTER TABLE tenants
    ADD COLUMN next_contact_number bigint NOT NULL DEFAULT 1 CHECK (next_contact_number > 0);

UPDATE tenants t
SET next_contact_number = COALESCE((SELECT MAX(c.number) + 1 FROM contacts c WHERE c.tenant_id = t.id), 1);

COMMENT ON COLUMN tenants.next_contact_number IS '工作区下一个新建联系人分配的递增编号';

-- +goose Down
ALTER TABLE tenants
    DROP COLUMN next_contact_number;

DROP INDEX contacts_tenant_number;

ALTER TABLE contacts
    DROP CONSTRAINT contacts_number_positive,
    DROP COLUMN number;
