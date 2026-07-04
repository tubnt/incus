-- +goose Up
-- PLAN-033 / OPS-039: store admin-side SSH credentials used to bootstrap new
-- cluster nodes (password or PEM private key). Ciphertext uses the same
-- AES-256-GCM v1: format as vms.password (OPS-022) — the encryption key is
-- shared via INCUS_PASSWORD_ENCRYPTION_KEY.
--
-- Out of scope: SSH agents, OpenSSH certificates, key rotation. The version
-- prefix in `ciphertext` leaves room for a future migration.

CREATE TABLE IF NOT EXISTS node_credentials (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('password','private_key')),
    ciphertext   TEXT NOT NULL,
    fingerprint  TEXT,
    created_by   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    UNIQUE (created_by, name)
);

CREATE INDEX IF NOT EXISTS idx_node_credentials_owner
    ON node_credentials (created_by, id DESC);

-- +goose Down
DROP TABLE IF EXISTS node_credentials;
