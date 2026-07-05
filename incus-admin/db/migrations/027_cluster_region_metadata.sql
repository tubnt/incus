-- +goose Up
-- PLAN-053 Phase C / INFRA-012：clusters 表加 region metadata 字段
--
-- /v1/regions 端点（cloud-gateway 适配层）需要把 cluster 暴露为标准 region：
--   id (cluster name) + country + city + status + capabilities[]
-- 现有 clusters 表只有运行时配置（cert/key/api_url），缺这 4 个对外元信息。
--
-- 字段说明：
--   country / city   ：国家 / 城市，nullable；初始留空，运维通过 admin UI 后续填
--   region_status    ：'available' | 'unavailable' | 'maintenance'
--                      默认 'available'，与 clusters.status 解耦
--                      （status 是内部健康；region_status 是对外可见的"是否开放下单"）
--   capabilities     ：JSONB 数组，对外能力声明。一期固定 ['instances']
--                      未来扩 ['instances','load-balancers','floating-ips'] 等
--
-- 不写回填 SQL：默认值 cover 所有现有行（country/city 留 NULL = 待填，
-- region_status='available' / capabilities='["instances"]' 均符合当前现状）。

ALTER TABLE clusters
    ADD COLUMN IF NOT EXISTS country        TEXT,
    ADD COLUMN IF NOT EXISTS city           TEXT,
    ADD COLUMN IF NOT EXISTS region_status  TEXT NOT NULL DEFAULT 'available'
        CHECK (region_status IN ('available','unavailable','maintenance')),
    ADD COLUMN IF NOT EXISTS capabilities   JSONB NOT NULL DEFAULT '["instances"]'::jsonb;

-- +goose Down
ALTER TABLE clusters DROP COLUMN IF EXISTS capabilities;
ALTER TABLE clusters DROP COLUMN IF EXISTS region_status;
ALTER TABLE clusters DROP COLUMN IF EXISTS city;
ALTER TABLE clusters DROP COLUMN IF EXISTS country;
