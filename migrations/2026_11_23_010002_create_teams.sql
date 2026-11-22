-- +goose Up
CREATE TABLE teams (
    id             uuid PRIMARY KEY,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    tenant_id      uuid NOT NULL REFERENCES tenants (id),
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    assignment     text NOT NULL DEFAULT 'manual',
    business_hours jsonb,
    disabled_at    timestamptz
);
CREATE UNIQUE INDEX teams_tenant_name ON teams (tenant_id, name);
COMMENT ON TABLE teams IS '团队收件箱：工作区中一组成员共同处理服务型会话的队列';
COMMENT ON COLUMN teams.id IS '主键';
COMMENT ON COLUMN teams.created_at IS '创建时间';
COMMENT ON COLUMN teams.updated_at IS '更新时间';
COMMENT ON COLUMN teams.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN teams.name IS '团队收件箱名称，同一工作区内唯一';
COMMENT ON COLUMN teams.description IS '团队收件箱说明';
COMMENT ON COLUMN teams.assignment IS '分配方式：manual（手动领取）或 round_robin（轮流分配）';
COMMENT ON COLUMN teams.business_hours IS '单独设置的时区与每周工作时间（hours.Schedule JSON）；为空表示使用工作区工作时间';
COMMENT ON COLUMN teams.disabled_at IS '停用时间，为空表示启用';

CREATE TABLE team_members (
    team_id          uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    member_id        uuid NOT NULL REFERENCES members (id),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    last_assigned_at timestamptz,
    PRIMARY KEY (team_id, member_id)
);
CREATE INDEX team_members_member ON team_members (member_id);
COMMENT ON TABLE team_members IS '团队收件箱与成员的多对多关联';
COMMENT ON COLUMN team_members.team_id IS '团队收件箱 ID';
COMMENT ON COLUMN team_members.member_id IS '成员 ID';
COMMENT ON COLUMN team_members.created_at IS '加入时间';
COMMENT ON COLUMN team_members.updated_at IS '更新时间';
COMMENT ON COLUMN team_members.last_assigned_at IS '轮流分配时最近一次在该团队分到会话的时间；为空表示尚未分到过';

-- +goose Down
DROP TABLE team_members;
DROP TABLE teams;
