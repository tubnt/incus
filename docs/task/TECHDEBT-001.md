# TECHDEBT-001 Close PLAN-009/010/011/012 deferred items

- **status**: completed
- **priority**: P1
- **owner**: claude
- **createdAt**: 2026-04-17 16:45
- **completedAt**: 2026-04-17 21:32
- **relatedPlan**: PLAN-013

## Summary

Aggregate all deferred items surfaced during the review of the last four plans and close them in PLAN-013.

## Scope

See PLAN-013 Phase A/B/C/D/E for the full breakdown. High-level:

- PLAN-011 code-level: handler-level IP rollback test, admin/vms.tsx pagination, product.Update real PATCH.
- PLAN-010 reverse-proxy: oauth2-proxy callback, security headers, favicon skip.
- PLAN-009 architecture: TLS fingerprint pinning, cluster ID hardcode removal, observability HTTPS iframe, dist staleness warning.
- PLAN-012 residual: CI integration test execution, atomic daily TopUp cap.

## Acceptance

- All items in PLAN-013 Phase A through D merged and deployed.
- PLAN-013 marked `[x]` in `docs/plan/index.md`.
- Follow-up tasks INFRA-004 / INFRA-005 filed and tracked to completion.
