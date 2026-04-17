# INFRA-004 Cluster TLS fingerprint pinning

- **status**: in_progress
- **priority**: P1
- **owner**: claude
- **createdAt**: 2026-04-17 16:45
- **relatedPlan**: PLAN-013 (Phase C.1)

## Summary

Replace `tls.InsecureSkipVerify=true` across cluster HTTP clients with SPKI fingerprint pinning.
Schema adds `clusters.tls_fingerprint`; first successful connect with empty value does
trust-on-first-use and writes the fingerprint back (with audit log). Subsequent connects must match.

## Scope

- `db/migrations/006_cluster_tls_pin.sql`: add `tls_fingerprint text` column.
- `internal/cluster/manager.go:190`: swap InsecureSkipVerify for `VerifyPeerCertificate` callback.
- `internal/handler/portal/events.go:68`: same swap for the events pipe.
- Admin UI: cluster detail shows fingerprint + "Reset fingerprint" action (destructive-confirm).
- Audit trail: TOFU learning events go to `audit_logs`.

## Acceptance

- Test cluster connects succeed, fingerprint stored.
- Tampered cert (swap with random self-signed) refuses connection and logs.
- Reset-fingerprint workflow round-trips.
