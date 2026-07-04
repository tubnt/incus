-- +goose Up
-- OPS-045: 为 OS 镜像模板目录补充 Windows 占位项。
--
-- 背景：linuxcontainers.org 不托管 Windows 镜像，无法像 Ubuntu / Debian 那样
-- 直接 `images:windows/...` 拉取。Windows VM 通常需要管理员先把 ISO 转换为
-- Incus image 上传：
--   incus image import windows-server-2022.qcow2 --alias windows-server-2022
-- 上传完成后，进入 admin/os-templates 把 source 改为本地 alias、protocol 改为
-- `incus`、server_url 清空，然后启用即可。
--
-- 这里 seed 两条 enabled=false 的占位条目，让"添加 Windows"成为一次后台
-- 配置而非代码改动；同时给前端 OS 选择器留好分组锚点。
INSERT INTO os_templates (slug, name, source, protocol, server_url, default_user, enabled, sort_order) VALUES
    ('windows-server-2022', 'Windows Server 2022', 'windows-server-2022', 'incus', '', 'Administrator', false, 200),
    ('windows-11',          'Windows 11',          'windows-11',          'incus', '', 'Administrator', false, 210)
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
DELETE FROM os_templates WHERE slug IN ('windows-server-2022', 'windows-11');
