-- +goose Up
-- Seed ids follow the fixed-literal convention of 0002_seed.up.sql.

INSERT INTO permissions (id, code, name)
VALUES
    ('00000000-0000-7000-8000-70000000000b', 'user:read', 'Read member accounts'),
    ('00000000-0000-7000-8000-70000000000c', 'audit:read', 'Read audit logs')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN ('user:read', 'audit:read')
WHERE r.name = 'super_admin'
ON CONFLICT DO NOTHING;
