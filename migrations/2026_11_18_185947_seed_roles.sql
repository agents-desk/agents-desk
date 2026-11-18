-- 预置的全局角色，ID 与 internal/auth 中的常量一致。权限必须在 auth.Catalog 中。
-- +goose Up
INSERT INTO authz_roles (id, tenant, name, description, created_at, updated_at) VALUES
    ('01900000-0000-7000-8000-000000000001', '', 'platform-admin', '平台管理员', '2026-11-19 00:00:00+00', '2026-11-19 00:00:00+00'),
    ('01900000-0000-7000-8000-000000000002', '', 'owner', '工作区的主管理员', '2026-11-19 00:00:00+00', '2026-11-19 00:00:00+00');

INSERT INTO authz_role_permissions (role_id, permission) VALUES
    ('01900000-0000-7000-8000-000000000001', 'tenants:read'),
    ('01900000-0000-7000-8000-000000000001', 'tenants:manage'),
    ('01900000-0000-7000-8000-000000000001', 'platform-settings:manage'),
    -- 主管理员在自己的工作区中拥有全部权限；PR 2 定下工作区的权限后改为明确的列表
    ('01900000-0000-7000-8000-000000000002', '*');

-- +goose Down
DELETE FROM authz_assignments WHERE role_id IN ('01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002');
DELETE FROM authz_role_permissions WHERE role_id IN ('01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002');
DELETE FROM authz_roles WHERE id IN ('01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002');
