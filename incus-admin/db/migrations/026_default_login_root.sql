-- +goose Up
-- OPS-051 / PLAN-052 Q7 决策：所有 cloud variant Linux 镜像统一 root 登录。
-- 跳过 Windows（保留 Administrator）与未来非 cloud variant 镜像。
UPDATE os_templates
SET default_user = 'root',
    updated_at = NOW()
WHERE source LIKE '%/cloud'
  AND slug NOT LIKE 'windows%';

-- +goose Down
-- 回滚：按 OS 家族还原原 default_user。仅覆盖 026 之前已存在的内置 11 模板；
-- 后续 admin 手动改的不会被 down 覆盖（如有应在 admin UI 手工调）。
UPDATE os_templates SET default_user = 'ubuntu'    WHERE slug IN ('ubuntu-24-04','ubuntu-22-04','ubuntu-20-04');
UPDATE os_templates SET default_user = 'debian'    WHERE slug IN ('debian-12','debian-11');
UPDATE os_templates SET default_user = 'rocky'     WHERE slug = 'rockylinux-9';
UPDATE os_templates SET default_user = 'almalinux' WHERE slug = 'almalinux-9';
UPDATE os_templates SET default_user = 'fedora'    WHERE slug = 'fedora-40';
UPDATE os_templates SET default_user = 'arch'      WHERE slug = 'archlinux';
