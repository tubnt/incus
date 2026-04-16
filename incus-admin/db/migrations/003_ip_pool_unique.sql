-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_ip_pools_cidr ON ip_pools(cidr);

-- +goose Down
DROP INDEX IF EXISTS idx_ip_pools_cidr;
