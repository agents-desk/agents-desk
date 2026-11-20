-- +goose Up
-- 团队的成员。一个人可以在多个团队中；lead 表示他是这个团队的组长，一个团队可以有多个组长。
-- 成员必须是团队所在工作区的成员：被移出工作区时，同一个事务中把他从这个工作区的团队中删除。
CREATE TABLE team_members (
    team_id    uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    member_id  uuid NOT NULL REFERENCES members (id),
    lead       boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (team_id, member_id)
);
CREATE INDEX team_members_member ON team_members (member_id);

-- +goose Down
DROP TABLE team_members;
