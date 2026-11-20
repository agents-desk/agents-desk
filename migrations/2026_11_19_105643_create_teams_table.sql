-- +goose Up
-- 团队：工作区中的一组成员，以后工单按团队进入队列、按团队的工作时间计算 SLA。名称在一个工作区中不重复。
CREATE TABLE teams (
    id          uuid PRIMARY KEY,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    name        text NOT NULL,
    description text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX teams_tenant_name ON teams (tenant_id, name);

-- +goose Down
DROP TABLE teams;
