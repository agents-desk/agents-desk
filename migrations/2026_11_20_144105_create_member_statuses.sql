-- +goose Up
-- 成员在工作区中的在线状态：online（在线，可以被自动分配）或 away（离开）。每个工作区分开设置；没有这一行就是离开。
-- reassign 是离开时勾选的“把我的会话重新分配”：对方再发来消息时，会话回到团队收件箱重新分配。
-- 成员离开工作区时，同一个事务中删除这一行。
CREATE TABLE member_statuses (
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    member_id  uuid NOT NULL REFERENCES members (id),
    status     text NOT NULL,
    reassign   boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, member_id)
);

-- +goose Down
DROP TABLE member_statuses;
