-- +goose Up
ALTER TABLE attribute_definitions
    ADD COLUMN ticket_type_id uuid REFERENCES ticket_types (id) ON DELETE CASCADE;

DROP INDEX attribute_definitions_tenant_target_key;
DROP INDEX attribute_definitions_tenant_target_pos;

ALTER TABLE attribute_definitions
    ADD CONSTRAINT attribute_definitions_ticket_type_check
        CHECK ((target = 'ticket') = (ticket_type_id IS NOT NULL));

CREATE UNIQUE INDEX attribute_definitions_tenant_target_key
    ON attribute_definitions (tenant_id, target, key)
    WHERE ticket_type_id IS NULL;

CREATE UNIQUE INDEX attribute_definitions_tenant_ticket_type_key
    ON attribute_definitions (tenant_id, ticket_type_id, key)
    WHERE ticket_type_id IS NOT NULL;

CREATE INDEX attribute_definitions_tenant_target_pos
    ON attribute_definitions (tenant_id, target, ticket_type_id, position, created_at);

COMMENT ON TABLE attribute_definitions IS '自定义属性定义：配置会话、联系人或工单类型的扩展字段';
COMMENT ON COLUMN attribute_definitions.target IS '属性所属对象类型（conversation、contact 或 ticket）';
COMMENT ON COLUMN attribute_definitions.ticket_type_id IS '所属工单类型 ID；仅当 target 为 ticket 时非空';

-- +goose Down
DROP INDEX attribute_definitions_tenant_target_pos;
DROP INDEX attribute_definitions_tenant_ticket_type_key;
DROP INDEX attribute_definitions_tenant_target_key;

ALTER TABLE attribute_definitions
    DROP CONSTRAINT attribute_definitions_ticket_type_check;

DELETE FROM attribute_definitions WHERE ticket_type_id IS NOT NULL;

ALTER TABLE attribute_definitions
    DROP COLUMN ticket_type_id;

CREATE UNIQUE INDEX attribute_definitions_tenant_target_key ON attribute_definitions (tenant_id, target, key);
CREATE INDEX attribute_definitions_tenant_target_pos ON attribute_definitions (tenant_id, target, position, created_at);
