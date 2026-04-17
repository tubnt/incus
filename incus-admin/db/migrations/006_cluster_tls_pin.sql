-- 006_cluster_tls_pin.sql —— cluster TLS SPKI fingerprint pinning
-- tls_fingerprint stores hex-encoded SHA256 of the peer cert's Subject Public
-- Key Info. NULL means "not yet learned" (first connect does TOFU and writes
-- back). A subsequent mismatch refuses the connection.
ALTER TABLE clusters
  ADD COLUMN IF NOT EXISTS tls_fingerprint text;
