-- +goose Up
-- PLAN-021 Phase A: os_templates
-- Moves the OS image catalog from hardcoded TypeScript (web/src/features/vms/os-image-picker.tsx)
-- into the database so admin can add new images without a code change.
-- Reinstall / Create flows will resolve template_slug -> source + default_user here.

CREATE TABLE IF NOT EXISTS os_templates (
    id                  SERIAL PRIMARY KEY,
    slug                TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL,
    source              TEXT NOT NULL,                            -- e.g. 'ubuntu/24.04/cloud'
    protocol            TEXT NOT NULL DEFAULT 'simplestreams',    -- 'simplestreams' | 'incus'
    server_url          TEXT NOT NULL DEFAULT 'https://images.linuxcontainers.org',
    default_user        TEXT NOT NULL DEFAULT 'ubuntu',
    cloud_init_template TEXT DEFAULT '',                          -- optional override per OS
    supports_rescue     BOOLEAN NOT NULL DEFAULT false,           -- reserved for Phase D
    enabled             BOOLEAN NOT NULL DEFAULT true,
    sort_order          INT NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_os_templates_enabled_sort ON os_templates(enabled, sort_order);

-- Seed the existing 4 hardcoded entries + 5 new ones (total 9). Slugs mirror the
-- current image string verbatim so older records pointing at images:ubuntu/24.04/cloud
-- keep working if anything reads the slug directly.
INSERT INTO os_templates (slug, name, source, default_user, sort_order) VALUES
    ('ubuntu-24-04',  'Ubuntu 24.04 LTS', 'ubuntu/24.04/cloud', 'ubuntu', 10),
    ('ubuntu-22-04',  'Ubuntu 22.04 LTS', 'ubuntu/22.04/cloud', 'ubuntu', 20),
    ('ubuntu-20-04',  'Ubuntu 20.04 LTS', 'ubuntu/20.04/cloud', 'ubuntu', 30),
    ('debian-12',     'Debian 12',        'debian/12/cloud',    'debian', 40),
    ('debian-11',     'Debian 11',        'debian/11/cloud',    'debian', 50),
    ('rockylinux-9',  'Rocky Linux 9',    'rockylinux/9/cloud', 'rocky',  60),
    ('almalinux-9',   'AlmaLinux 9',      'almalinux/9/cloud',  'almalinux', 70),
    ('fedora-40',     'Fedora 40',        'fedora/40/cloud',    'fedora', 80),
    ('archlinux',     'Arch Linux',       'archlinux/current/cloud', 'arch', 90)
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS os_templates;
