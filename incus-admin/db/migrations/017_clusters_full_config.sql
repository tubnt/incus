-- +goose Up
-- PLAN-027 / INFRA-003：clusters 表扩 schema 支持完整 cluster + standalone host 配置
--
-- 设计：
--   - cert_file / key_file / ca_file：admin 服务器本地路径，nullable（env 兜底场景下可空）
--   - kind：'cluster' = 多节点 Incus 集群（默认）；'standalone' = 单节点 Incus 主机
--   - default_project / storage_pool / network：原 env 里硬编码的运行时默认值
--   - ip_pools：JSONB 数组，结构与 config.IPPoolConfig 对应
--
-- 不存 TLS cert/key 内容（避免密钥落 DB），仅存路径。

ALTER TABLE clusters ADD COLUMN IF NOT EXISTS cert_file TEXT;
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS key_file TEXT;
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS ca_file TEXT;
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'cluster'
    CHECK (kind IN ('cluster','standalone'));
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS default_project TEXT;
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS storage_pool TEXT;
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS network TEXT;
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS ip_pools_json JSONB;

-- +goose Down
ALTER TABLE clusters DROP COLUMN IF EXISTS ip_pools_json;
ALTER TABLE clusters DROP COLUMN IF EXISTS network;
ALTER TABLE clusters DROP COLUMN IF EXISTS storage_pool;
ALTER TABLE clusters DROP COLUMN IF EXISTS default_project;
ALTER TABLE clusters DROP COLUMN IF EXISTS kind;
ALTER TABLE clusters DROP COLUMN IF EXISTS ca_file;
ALTER TABLE clusters DROP COLUMN IF EXISTS key_file;
ALTER TABLE clusters DROP COLUMN IF EXISTS cert_file;
