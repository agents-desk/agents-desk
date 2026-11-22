-- +goose Up
CREATE TABLE contacts (
    id               uuid PRIMARY KEY,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    tenant_id        uuid NOT NULL REFERENCES tenants (id),
    number           bigint NOT NULL,
    kind             text NOT NULL,
    name             text NOT NULL DEFAULT '',
    email            text NOT NULL DEFAULT '',
    external_user_id text NOT NULL DEFAULT '',
    language         text NOT NULL DEFAULT '',
    language_manual  boolean NOT NULL DEFAULT false,
    attributes       jsonb NOT NULL DEFAULT '{}',
    notes            text NOT NULL DEFAULT '',
    location         text NOT NULL DEFAULT '',
    time_zone        text NOT NULL DEFAULT '',
    referrer         text NOT NULL DEFAULT '',
    last_seen_at     timestamptz,
    CONSTRAINT contacts_number_positive CHECK (number > 0)
);
CREATE INDEX contacts_tenant ON contacts (tenant_id, created_at);
CREATE UNIQUE INDEX contacts_tenant_number ON contacts (tenant_id, number);
CREATE UNIQUE INDEX contacts_tenant_user ON contacts (tenant_id, external_user_id) WHERE external_user_id <> '';
CREATE INDEX contacts_tenant_kind ON contacts (tenant_id, kind, created_at DESC);
CREATE INDEX contacts_tenant_last_seen ON contacts (tenant_id, COALESCE(last_seen_at, created_at) DESC, id DESC);
COMMENT ON TABLE contacts IS '联系人：与工作区沟通的外部客户或访客线索';
COMMENT ON COLUMN contacts.id IS '主键';
COMMENT ON COLUMN contacts.created_at IS '创建时间';
COMMENT ON COLUMN contacts.updated_at IS '更新时间';
COMMENT ON COLUMN contacts.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN contacts.number IS '联系人在工作区内的递增编号，从 1 开始，创建后不变';
COMMENT ON COLUMN contacts.kind IS '联系人类型：user（已识别用户）或 lead（访客线索）';
COMMENT ON COLUMN contacts.name IS '联系人姓名或昵称';
COMMENT ON COLUMN contacts.email IS '联系人邮箱';
COMMENT ON COLUMN contacts.external_user_id IS '业务产品传入的外部用户编号，同一工作区内非空时唯一';
COMMENT ON COLUMN contacts.language IS '联系人的沟通语言（BCP 47 标签）；为空表示尚未识别';
COMMENT ON COLUMN contacts.language_manual IS '语言是否由成员手动指定；为 true 时来消息不再自动覆盖';
COMMENT ON COLUMN contacts.attributes IS '联系人自定义属性值（JSON 对象）';
COMMENT ON COLUMN contacts.notes IS '成员填写的联系人内部备注，仅成员可见';
COMMENT ON COLUMN contacts.location IS '联系人所在地';
COMMENT ON COLUMN contacts.time_zone IS '联系人所在的 IANA 时区名，用于显示对方当地时间';
COMMENT ON COLUMN contacts.referrer IS '联系人首次访问的来源页面地址';
COMMENT ON COLUMN contacts.last_seen_at IS '联系人最近活跃或联系的时间';

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
COMMENT ON TABLE channels IS '接入渠道：工作区收发外部消息的具体入口（如网页聊天插件、测试渠道）';
COMMENT ON COLUMN channels.id IS '主键';
COMMENT ON COLUMN channels.created_at IS '创建时间';
COMMENT ON COLUMN channels.updated_at IS '更新时间';
COMMENT ON COLUMN channels.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN channels.kind IS '渠道类型，决定由哪个连接器收发消息';
COMMENT ON COLUMN channels.name IS '渠道名称';
COMMENT ON COLUMN channels.settings IS '渠道连接器配置（JSON 对象）';
COMMENT ON COLUMN channels.team_id IS '新会话默认进入的团队收件箱 ID；为空表示进入未指派';
COMMENT ON COLUMN channels.enabled IS '是否启用该渠道';

CREATE TABLE contact_identities (
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    channel_id  uuid NOT NULL REFERENCES channels (id),
    external_id text NOT NULL,
    contact_id  uuid NOT NULL REFERENCES contacts (id),
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (channel_id, external_id)
);
CREATE INDEX contact_identities_contact ON contact_identities (contact_id);
COMMENT ON TABLE contact_identities IS '联系人在各接入渠道中的外部身份映射';
COMMENT ON COLUMN contact_identities.tenant_id IS '所属工作区 ID';
COMMENT ON COLUMN contact_identities.channel_id IS '渠道 ID';
COMMENT ON COLUMN contact_identities.external_id IS '联系人在该渠道中的外部标识';
COMMENT ON COLUMN contact_identities.contact_id IS '关联的联系人 ID';
COMMENT ON COLUMN contact_identities.created_at IS '创建时间';

-- +goose Down
DROP TABLE contact_identities;
DROP TABLE channels;
DROP TABLE contacts;
