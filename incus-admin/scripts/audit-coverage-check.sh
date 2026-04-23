#!/usr/bin/env bash
# PLAN-019 Phase B — handler-level audit() coverage check.
#
# Middleware (internal/middleware/auditwrite.go) already writes a coarse
# `http.<METHOD>` audit_logs row for every admin write. This script monitors
# the *business-level* audit() coverage inside handlers — calls to `audit(...)`
# that encode resource/action semantics (e.g. `vm.delete`, `node.evacuate`).
#
# A handler file with writes > 0 but audits == 0 is a strong candidate for
# missing business-level audit calls. This is a heuristic (audit count is not
# 1:1 with routes) but catches obvious gaps quickly.
#
# Exit code: 0 on report only (informational). CI can run with `--strict` to
# fail when any handler has writes > 0 && audits == 0.
#
# Usage:
#   scripts/audit-coverage-check.sh            # report-only
#   scripts/audit-coverage-check.sh --strict   # CI mode: fail on gaps

set -euo pipefail

STRICT=0
if [[ "${1:-}" == "--strict" ]]; then
  STRICT=1
fi

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
HANDLER_DIR="$REPO_ROOT/internal/handler/portal"

if [[ ! -d "$HANDLER_DIR" ]]; then
  echo "handler dir not found: $HANDLER_DIR" >&2
  exit 2
fi

GAPS=0
TOTAL_WRITES=0
TOTAL_AUDITS=0

printf '%-32s %8s %8s %s\n' 'file' 'writes' 'audits' 'status'
printf '%-32s %8s %8s %s\n' '----' '------' '------' '------'

for f in "$HANDLER_DIR"/*.go; do
  base=$(basename "$f")
  [[ "$base" == *_test.go ]] && continue

  # Count *unique* handler references among write-method route
  # registrations. Extracting the last identifier inside the paren group
  # folds admin+portal double-registration of the same method (common
  # pattern in snapshot.go) into one "write" so the ratio stays meaningful.
  # `|| true` on every grep so set -e doesn't abort on zero-match files.
  routes=$(grep -oE 'r\.(Post|Put|Patch|Delete)\([^)]*\)' "$f" 2>/dev/null || true)
  if [[ -z "$routes" ]]; then
    writes=0
  else
    writes=$(echo "$routes" \
      | grep -oE '[A-Za-z_][A-Za-z0-9_]*[[:space:]]*\)' \
      | sed -E 's/[[:space:]]*\).*//' \
      | sort -u \
      | wc -l)
  fi
  audits=$(grep -cE '\baudit\(' "$f" 2>/dev/null || echo 0)

  if [[ "$writes" -eq 0 ]]; then
    continue
  fi

  TOTAL_WRITES=$((TOTAL_WRITES + writes))
  TOTAL_AUDITS=$((TOTAL_AUDITS + audits))

  status='ok'
  if [[ "$audits" -eq 0 ]]; then
    status='MISSING'
    GAPS=$((GAPS + 1))
  elif [[ "$audits" -lt "$writes" ]]; then
    status='partial'
  fi

  printf '%-32s %8d %8d %s\n' "$base" "$writes" "$audits" "$status"
done

echo
printf 'totals: writes=%d audits=%d missing-files=%d\n' "$TOTAL_WRITES" "$TOTAL_AUDITS" "$GAPS"

if [[ "$STRICT" -eq 1 && "$GAPS" -gt 0 ]]; then
  echo "audit coverage check FAILED: $GAPS handler files have writes but no audit() calls" >&2
  exit 1
fi
exit 0
