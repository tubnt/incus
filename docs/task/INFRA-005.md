# INFRA-005 Observability iframe HTTPS reverse proxy

- **status**: pending
- **priority**: P2
- **owner**: claude
- **createdAt**: 2026-04-17 16:45
- **relatedPlan**: PLAN-013 (Phase C.3)

## Summary

The admin Observability page iframes Grafana / Prometheus / Alertmanager on `http://10.0.20.1:*`.
Browsers block Mixed-Content from an HTTPS site. Reverse-proxy them through Caddy as
`https://vmc.5ok.co/observability/{grafana,prometheus,alertmanager}/` and update the frontend
`DASHBOARDS` URLs to relative paths.

## Scope

- Caddy reverse proxy to the three upstreams with correct sub-path handling
  (Grafana `GF_SERVER_SERVE_FROM_SUB_PATH=true` + `GF_SERVER_ROOT_URL`, Prometheus
  `--web.external-url`, Alertmanager `--web.external-url`).
- Frontend `web/src/app/routes/admin/observability.tsx` DASHBOARDS array uses relative paths.
- Fallback: if any of the three does not support sub-path, switch to new-window link instead of iframe.

## Acceptance

- iframe loads without Mixed-Content warning.
- SSO already gates the page (oauth2-proxy), so no additional auth needed in reverse-proxy layer.
