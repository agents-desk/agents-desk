-- +goose Up
CREATE TABLE channels (
    id         uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    kind       text NOT NULL,
    name       text NOT NULL,
    settings   jsonb NOT NULL DEFAULT '{}',
    team_id    uuid REFERENCES teams (id) ON DELETE SET NULL,
    enabled    boolean NOT NULL DEFAULT true
);

CREATE INDEX channels_tenant ON channels (tenant_id);

COMMENT ON TABLE channels IS '接入渠道：工作区收发外部消息的具体入口（如网页聊天插件、Telegram 机器人、测试渠道）';
COMMENT ON COLUMN channels.id IS '主键';
COMMENT ON COLUMN channels.created_at IS '创建时间';
COMMENT ON COLUMN channels.updated_at IS '更新时间';
COMMENT ON COLUMN channels.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN channels.kind IS '渠道类型，决定由哪个连接器收发消息';
COMMENT ON COLUMN channels.name IS '渠道名称';
COMMENT ON COLUMN channels.settings IS '渠道连接器配置（JSON 对象）';
COMMENT ON COLUMN channels.team_id IS '新会话默认进入的团队收件箱 ID；为空表示进入未指派';
COMMENT ON COLUMN channels.enabled IS '是否启用该渠道';

-- +goose Down
DROP TABLE channels;
