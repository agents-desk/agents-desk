-- +goose Up
ALTER TABLE tenants
    ADD COLUMN next_ticket_number bigint NOT NULL DEFAULT 1 CHECK (next_ticket_number > 0);

COMMENT ON COLUMN tenants.next_ticket_number IS '工作区下一个新建工单分配的递增编号';

-- +goose Down
ALTER TABLE tenants
    DROP COLUMN next_ticket_number;
