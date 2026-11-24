-- +goose Up
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
