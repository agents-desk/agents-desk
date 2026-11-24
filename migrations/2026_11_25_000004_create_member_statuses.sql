-- +goose Up
CREATE TABLE member_statuses (
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    member_id  uuid NOT NULL REFERENCES members (id),
    status     text NOT NULL,
    reassign   boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, member_id)
);

COMMENT ON TABLE member_statuses IS '成员在各工作区中的在线状态与离开重分配偏好';
COMMENT ON COLUMN member_statuses.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN member_statuses.member_id IS '成员 ID';
COMMENT ON COLUMN member_statuses.status IS '在线状态：online（在线）或 away（离开）';
COMMENT ON COLUMN member_statuses.reassign IS '离开时是否将名下会话回到团队收件箱重新分配';
COMMENT ON COLUMN member_statuses.updated_at IS '状态更新时间';

-- +goose Down
DROP TABLE member_statuses;
