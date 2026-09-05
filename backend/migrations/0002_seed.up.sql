-- +goose Up
-- Seed rows bypass the service-layer generator: primary keys are fixed UUID
-- literals, generated once and frozen with the migration
-- (docs/architecture/08-uuid-primary-keys.md §3).

INSERT INTO roles (id, name, remark)
VALUES
    ('00000000-0000-7000-8000-000000000001', 'super_admin', 'Full administrator access'),
    ('00000000-0000-7000-8000-000000000002', 'operator', 'Operational administrator access')
ON CONFLICT (name) DO NOTHING;

INSERT INTO permissions (id, code, name)
VALUES
    ('00000000-0000-7000-8000-700000000001', 'product:read', 'Read products'),
    ('00000000-0000-7000-8000-700000000002', 'product:write', 'Write products'),
    ('00000000-0000-7000-8000-700000000003', 'image:write', 'Upload images'),
    ('00000000-0000-7000-8000-700000000004', 'order:read', 'Read orders'),
    ('00000000-0000-7000-8000-700000000005', 'order:ship', 'Ship orders'),
    ('00000000-0000-7000-8000-700000000006', 'refund:read', 'Read refund requests'),
    ('00000000-0000-7000-8000-700000000007', 'refund:approve', 'Approve or reject refunds'),
    ('00000000-0000-7000-8000-700000000008', 'points:adjust', 'Adjust buyer points'),
    ('00000000-0000-7000-8000-700000000009', 'role:manage', 'Manage roles and administrators'),
    ('00000000-0000-7000-8000-70000000000a', 'admin:self', 'Manage own administrator session')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.name = 'super_admin'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN (
    'product:read',
    'product:write',
    'image:write',
    'order:read',
    'order:ship',
    'refund:read',
    'admin:self'
)
WHERE r.name = 'operator'
ON CONFLICT DO NOTHING;
