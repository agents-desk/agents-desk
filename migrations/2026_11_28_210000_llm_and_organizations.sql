-- +goose Up
-- 扩展模型调用归因并建立工作区客户公司/组织（organizations）一等实体：
-- 1. 新建 organizations 表并建立工作区内唯一名称与外部编号索引；
-- 2. 补全 external_groups.organization_id 与 contacts.organization_id 外键及索引；
-- 3. 补全 llm_calls.generation_id 归因外键与索引。

CREATE TABLE organizations (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        text NOT NULL,
    domain      text NOT NULL DEFAULT '',
    external_id text NOT NULL DEFAULT '',
    notes       text NOT NULL DEFAULT '',
    attributes  jsonb NOT NULL DEFAULT '{}'
);

CREATE UNIQUE INDEX organizations_tenant_name ON organizations (tenant_id, lower(name));
CREATE UNIQUE INDEX organizations_tenant_external_id ON organizations (tenant_id, external_id) WHERE external_id <> '';
CREATE INDEX organizations_tenant_created ON organizations (tenant_id, created_at DESC, id DESC);

COMMENT ON TABLE organizations IS '客户公司/组织：按客户主体聚合联系人和外部协作群的企业实体';
COMMENT ON COLUMN organizations.id IS '主键';
COMMENT ON COLUMN organizations.created_at IS '创建时间';
COMMENT ON COLUMN organizations.updated_at IS '更新时间';
COMMENT ON COLUMN organizations.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN organizations.name IS '组织名称（同一工作区内不区分大小写唯一）';
COMMENT ON COLUMN organizations.domain IS '组织主域名（如 acme.com）';
COMMENT ON COLUMN organizations.external_id IS '业务系统的外部客户编号（非空时同一工作区内唯一）';
COMMENT ON COLUMN organizations.notes IS '组织备注信息';
COMMENT ON COLUMN organizations.attributes IS '组织的扩展属性（JSON 对象）';

UPDATE external_groups SET organization_id = NULL WHERE organization_id IS NOT NULL;
ALTER TABLE external_groups
    ADD CONSTRAINT external_groups_organization_id_fkey
        FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE SET NULL;
CREATE INDEX external_groups_tenant_org ON external_groups (tenant_id, organization_id) WHERE organization_id IS NOT NULL;

ALTER TABLE contacts
    ADD COLUMN organization_id uuid REFERENCES organizations (id) ON DELETE SET NULL;
CREATE INDEX contacts_tenant_org ON contacts (tenant_id, organization_id) WHERE organization_id IS NOT NULL;

COMMENT ON COLUMN contacts.organization_id IS '所属客户公司/组织 ID';

ALTER TABLE llm_calls
    ADD COLUMN generation_id uuid REFERENCES generations (id) ON DELETE SET NULL;
CREATE INDEX llm_calls_generation ON llm_calls (generation_id) WHERE generation_id IS NOT NULL;

COMMENT ON COLUMN llm_calls.generation_id IS '关联的 AI 生成记录 ID；非 Agent 生成调用为空';

-- +goose Down
DROP INDEX IF EXISTS llm_calls_generation;
ALTER TABLE llm_calls DROP COLUMN IF EXISTS generation_id;
DROP INDEX IF EXISTS contacts_tenant_org;
ALTER TABLE contacts DROP COLUMN IF EXISTS organization_id;
DROP INDEX IF EXISTS external_groups_tenant_org;
ALTER TABLE external_groups DROP CONSTRAINT IF EXISTS external_groups_organization_id_fkey;
DROP TABLE IF EXISTS organizations;
