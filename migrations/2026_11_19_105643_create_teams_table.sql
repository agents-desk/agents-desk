-- +goose Up
-- 团队收件箱：工作区中一组成员共同处理服务型会话的队列，名称在一个工作区中不重复。
-- assignment 是分配方式：manual（成员自己领取）或 round_robin（轮流分给在线的成员）。
CREATE TABLE teams (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    assignment  text NOT NULL DEFAULT 'manual'
);
CREATE UNIQUE INDEX teams_tenant_name ON teams (tenant_id, name);

-- +goose Down
DROP TABLE teams;
