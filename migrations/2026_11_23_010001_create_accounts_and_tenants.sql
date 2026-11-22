-- +goose Up
CREATE TABLE platform_admins (
    id             uuid PRIMARY KEY,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    name           text NOT NULL,
    avatar_file_id uuid REFERENCES files (id),
    disabled_at    timestamptz
);
COMMENT ON TABLE platform_admins IS '平台管理员：负责整套部署，登录管理员端；邮箱和密码在 auth_* 表中';
COMMENT ON COLUMN platform_admins.id IS '主键';
COMMENT ON COLUMN platform_admins.created_at IS '创建时间';
COMMENT ON COLUMN platform_admins.updated_at IS '更新时间';
COMMENT ON COLUMN platform_admins.name IS '显示名称';
COMMENT ON COLUMN platform_admins.avatar_file_id IS '头像文件 ID，关联 files.id；为空表示未设置头像';
COMMENT ON COLUMN platform_admins.disabled_at IS '非空表示已停用，不能再登录';

CREATE TABLE members (
    id             uuid PRIMARY KEY,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    name           text NOT NULL,
    avatar_file_id uuid REFERENCES files (id),
    language       text NOT NULL DEFAULT 'zh-CN',
    time_zone      text NOT NULL DEFAULT '',
    disabled_at    timestamptz
);
COMMENT ON TABLE members IS '工作区成员的全局账号：一个人可以属于多个租户，在各租户中的角色在 authz_assignments 中；邮箱和密码在 auth_* 表中';
COMMENT ON COLUMN members.id IS '主键';
COMMENT ON COLUMN members.created_at IS '创建时间';
COMMENT ON COLUMN members.updated_at IS '更新时间';
COMMENT ON COLUMN members.name IS '显示名称';
COMMENT ON COLUMN members.avatar_file_id IS '头像文件 ID，关联 files.id；为空表示未设置头像';
COMMENT ON COLUMN members.language IS '界面与译文偏好语言（BCP 47 标签，如 zh-CN、en）';
COMMENT ON COLUMN members.time_zone IS 'IANA 时区名称；为空表示跟随浏览器时区';
COMMENT ON COLUMN members.disabled_at IS '非空表示已停用，不能再登录任何工作区';

CREATE TABLE tenants (
    id                  uuid PRIMARY KEY,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    slug                text NOT NULL,
    name                text NOT NULL,
    owner_id            uuid NOT NULL REFERENCES members (id),
    disabled_at         timestamptz,
    reopen_window_hours integer NOT NULL DEFAULT 24 CHECK (reopen_window_hours >= 0),
    next_contact_number bigint NOT NULL DEFAULT 1 CHECK (next_contact_number > 0),
    CONSTRAINT tenants_slug_unique UNIQUE (slug)
);
CREATE INDEX tenants_owner_id ON tenants (owner_id);
CREATE INDEX tenants_created_at ON tenants (created_at, id);
COMMENT ON TABLE tenants IS '租户，即产品中的工作区。id 是 authz 的域和业务表中的 tenant_id';
COMMENT ON COLUMN tenants.id IS '主键，同时作为 authz 域和业务表的 tenant_id';
COMMENT ON COLUMN tenants.created_at IS '创建时间';
COMMENT ON COLUMN tenants.updated_at IS '更新时间';
COMMENT ON COLUMN tenants.slug IS '地址：/t/{slug}/，创建后不能修改';
COMMENT ON COLUMN tenants.name IS '工作区名称';
COMMENT ON COLUMN tenants.owner_id IS '主管理员，同时在 authz 中拥有这个租户的 owner 角色，两者在同一个事务中修改';
COMMENT ON COLUMN tenants.disabled_at IS '非空表示已停用：成员不能进入，数据保留';
COMMENT ON COLUMN tenants.reopen_window_hours IS '单窗口渠道会话关闭后的重开窗口（小时），0 表示关闭后直接新建会话';
COMMENT ON COLUMN tenants.next_contact_number IS '工作区下一个新建联系人分配的递增编号';

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
DROP TABLE tenants;
DROP TABLE members;
DROP TABLE platform_admins;
