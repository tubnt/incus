#!/usr/bin/env bash
# PLAN-053 / INFRA-012 cloud-gateway /v1 端到端覆盖测试
#
# 目标：跑完 12 个 /v1 端点 + 5 类错误路径 + Idempotency-Key 重放 + 限流，
# 让运维与 cloud-gateway provider 团队按本脚本逐条验证契约。
#
# 用法：
#   export INCUSADMIN_BASE="http://localhost:8080/api"     # 必填
#   export INCUSADMIN_TOKEN="ica_xxxxxxxxxxxxxxxx..."      # 必填（portal /api-tokens 生成）
#   export INCUSADMIN_REGION="incus-cluster-01"            # 可选；缺省取 /v1/regions 第一个 available
#   export INCUSADMIN_TYPE_SLUG="small"                    # 可选；缺省取 /v1/types 第一个有 daily price 的
#   export INCUSADMIN_IMAGE_SLUG="ubuntu-2404"             # 可选；缺省取 /v1/images 第一个
#   export INCUSADMIN_E2E_CREATE=1                         # 1=跑 create/poll/delete；0=只读路径
#   bash scripts/e2e-cloud-gateway.sh
#
# 退出码：0 全部通过；1 某一断言失败；2 前置变量缺失。
#
# 本脚本只用 curl + jq，避免引外部依赖；不直接 jq 解 JSON 时退回 grep。

set -euo pipefail

: "${INCUSADMIN_BASE:?需要设置 INCUSADMIN_BASE (e.g. https://vmc.5ok.co/api)}"
: "${INCUSADMIN_TOKEN:?需要设置 INCUSADMIN_TOKEN (Bearer ica_xxx)}"

# 通用 curl 包装：-sS 不输出进度但保留 error；-o 捕获 body，-w 捕获 status。
H_AUTH=(-H "Authorization: Bearer ${INCUSADMIN_TOKEN}")
H_JSON=(-H "Accept: application/json")

step=0
pass=0
fail=0

log() { printf '\033[36m[%s]\033[0m %s\n' "$(date +%H:%M:%S)" "$*"; }
ok()  { pass=$((pass+1)); printf '  \033[32m✓\033[0m %s\n' "$*"; }
ko()  { fail=$((fail+1)); printf '  \033[31m✗\033[0m %s\n' "$*"; }
sub() { step=$((step+1)); printf '\n\033[1;33m── step %d · %s ──\033[0m\n' "$step" "$*"; }

have_jq=0
if command -v jq >/dev/null 2>&1; then have_jq=1; fi

# req METHOD PATH [extra curl flags...]
# 输出："<status>\n<body>"；调用方用 head/tail 拆。
req() {
  local method="$1" path="$2"
  shift 2
  local body status tmp
  tmp="$(mktemp)"
  status=$(curl -sS -o "$tmp" -w '%{http_code}' \
    -X "$method" "${H_AUTH[@]}" "${H_JSON[@]}" "$@" "${INCUSADMIN_BASE}${path}")
  body=$(cat "$tmp"); rm -f "$tmp"
  printf '%s\n%s' "$status" "$body"
}

# assert_status EXPECTED ACTUAL_OUTPUT
assert_status() {
  local want="$1" got
  got=$(printf '%s' "$2" | head -n1)
  if [[ "$got" == "$want" ]]; then
    ok "status=$got"
  else
    ko "status=$got, want $want (body: $(printf '%s' "$2" | tail -n+2 | head -c 200))"
  fi
}

# 取 body part（第二行起到末尾）
body_of() { printf '%s' "$1" | tail -n+2; }

# 解 JSON 字段（jq 优先，否则 grep）。仅适合扁平字段提取。
field() {
  local json="$1" key="$2"
  if [[ $have_jq -eq 1 ]]; then
    printf '%s' "$json" | jq -r ".${key} // empty"
  else
    # naive：扁平字段 "<key>":"<val>" or "<key>":<num>
    printf '%s' "$json" | grep -oE "\"${key}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"|\"${key}\"[[:space:]]*:[[:space:]]*[0-9.]+" \
      | head -1 \
      | sed -E "s/\"${key}\"[[:space:]]*:[[:space:]]*//; s/^\"//; s/\"$//"
  fi
}

# 取 data[0].field （仅 jq 模式才精确，否则启发式取第一个 "<key>"）
first_field() {
  local json="$1" key="$2"
  if [[ $have_jq -eq 1 ]]; then
    printf '%s' "$json" | jq -r ".data[0].${key} // empty"
  else
    printf '%s' "$json" | grep -oE "\"${key}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" \
      | head -1 \
      | sed -E "s/\"${key}\"[[:space:]]*:[[:space:]]*//; s/^\"//; s/\"$//"
  fi
}

############################################################################
# 1. /v1/account
############################################################################
sub "GET /v1/account"
out=$(req GET /v1/account)
assert_status 200 "$out"
body=$(body_of "$out")
balance=$(field "$body" balance)
log "balance=${balance} currency=$(field "$body" currency)"
runway=$(field "$body" estimated_runway_days)
if [[ -n "$runway" ]]; then ok "estimated_runway_days=$runway (active sub 存在)"; else ok "estimated_runway_days 省略（无 active sub / balance≤0）"; fi

############################################################################
# 2. /v1/types （含 prices.daily）
############################################################################
sub "GET /v1/types"
out=$(req GET "/v1/types?page=1&page_size=10")
assert_status 200 "$out"
body=$(body_of "$out")
total=$(field "$body" total)
log "types total=$total"

# 选 type slug
type_slug="${INCUSADMIN_TYPE_SLUG:-}"
if [[ -z "$type_slug" && $have_jq -eq 1 ]]; then
  type_slug=$(printf '%s' "$body" | jq -r '.data[] | select(.prices.daily != null) | .slug' | head -1)
  if [[ -z "$type_slug" ]]; then type_slug=$(first_field "$body" slug); fi
fi
log "选用 type_slug=${type_slug:-<unset>}"

############################################################################
# 3. /v1/regions
############################################################################
sub "GET /v1/regions"
out=$(req GET /v1/regions)
assert_status 200 "$out"
body=$(body_of "$out")
region="${INCUSADMIN_REGION:-}"
if [[ -z "$region" && $have_jq -eq 1 ]]; then
  region=$(printf '%s' "$body" | jq -r '.data[] | select(.status == "available") | .id' | head -1)
  if [[ -z "$region" ]]; then region=$(first_field "$body" id); fi
fi
log "选用 region=${region:-<unset>}"

############################################################################
# 4. /v1/images
############################################################################
sub "GET /v1/images"
out=$(req GET /v1/images)
assert_status 200 "$out"
body=$(body_of "$out")
image_slug="${INCUSADMIN_IMAGE_SLUG:-}"
if [[ -z "$image_slug" ]]; then image_slug=$(first_field "$body" id); fi
log "选用 image=${image_slug:-<unset>}"

############################################################################
# 5. /v1/ssh-keys
############################################################################
sub "GET /v1/ssh-keys"
out=$(req GET /v1/ssh-keys)
assert_status 200 "$out"

############################################################################
# 6. /v1/instances list（read-only，无 VM 也 OK，total>=0）
############################################################################
sub "GET /v1/instances"
out=$(req GET "/v1/instances?page=1&page_size=10")
assert_status 200 "$out"

############################################################################
# 7. 错误路径
############################################################################
sub "错误路径：缺 Authorization → 401"
status=$(curl -sS -o /dev/null -w '%{http_code}' -X GET "${INCUSADMIN_BASE}/v1/account")
if [[ "$status" == "401" ]]; then ok "401"; else ko "got $status"; fi

sub "错误路径：page_size 非法 → 422"
out=$(req GET "/v1/types?page_size=abc")
assert_status 422 "$out"

sub "错误路径：invalid Idempotency-Key → 422"
out=$(req POST /v1/instances -H "Idempotency-Key: short" -H "Content-Type: application/json" -d '{}')
# 422 reason=invalid（Idempotency middleware 拦下）；也可能是 422 不同 field 的
# 校验先于 idempotency（顺序看 server）。这里只断状态码 422。
assert_status 422 "$out"

############################################################################
# 8. create / poll / actions / delete（受 INCUSADMIN_E2E_CREATE 控）
############################################################################
if [[ "${INCUSADMIN_E2E_CREATE:-0}" != "1" ]]; then
  log "INCUSADMIN_E2E_CREATE != 1，跳过 create/poll/delete 链路；只读路径已覆盖。"
else
  if [[ -z "$region" || -z "$type_slug" || -z "$image_slug" ]]; then
    log "缺 region/type/image，无法跑创建链路；export 之后重跑。"
  else
    sub "POST /v1/instances + Idempotency-Key 一次创建"
    idem_key="e2e-$(date +%s)-$(printf '%016x' "$RANDOM$RANDOM")"
    out=$(req POST /v1/instances \
      -H "Idempotency-Key: $idem_key" \
      -H "Content-Type: application/json" \
      -d "{
        \"region\": \"$region\",
        \"type\": \"$type_slug\",
        \"image\": \"$image_slug\",
        \"label\": \"e2e-test-$RANDOM\",
        \"period\": \"daily\"
      }")
    assert_status 201 "$out"
    body=$(body_of "$out")
    INSTANCE_ID=$(field "$body" id)
    log "创建到 INSTANCE_ID=$INSTANCE_ID order_id=$(field "$body" order_id) job_id=$(field "$body" job_id)"

    sub "POST /v1/instances 同 key 同 payload → 命中重放 (Idempotent-Replay: true)"
    replay_header=$(curl -sS -D - -o /dev/null -X POST \
      "${H_AUTH[@]}" -H "Idempotency-Key: $idem_key" -H "Content-Type: application/json" \
      -d "{
        \"region\": \"$region\",
        \"type\": \"$type_slug\",
        \"image\": \"$image_slug\",
        \"label\": \"e2e-test-replay\",
        \"period\": \"daily\"
      }" "${INCUSADMIN_BASE}/v1/instances" | tr -d '\r' | grep -i 'idempotent-replay' || true)
    if [[ -n "$replay_header" ]]; then ok "$replay_header"; else ko "未找到 Idempotent-Replay 头"; fi

    sub "轮询 GET /v1/instances/$INSTANCE_ID 直到 running (timeout 600s)"
    deadline=$(( $(date +%s) + 600 ))
    while true; do
      out=$(req GET "/v1/instances/$INSTANCE_ID")
      assert_status 200 "$out" >/dev/null || true
      body=$(body_of "$out")
      st=$(field "$body" status)
      log "  ... status=$st"
      if [[ "$st" == "running" ]]; then ok "running"; break; fi
      if [[ "$st" == "error" || "$st" == "deleted" || "$st" == "gone" ]]; then ko "终态 $st"; break; fi
      if (( $(date +%s) > deadline )); then ko "timeout @ $st"; break; fi
      sleep 5
    done

    sub "POST /v1/instances/$INSTANCE_ID/reboot → 202"
    out=$(req POST "/v1/instances/$INSTANCE_ID/reboot")
    assert_status 202 "$out"

    sub "POST /v1/instances/$INSTANCE_ID/shutdown → 202"
    out=$(req POST "/v1/instances/$INSTANCE_ID/shutdown")
    assert_status 202 "$out"

    sub "POST /v1/instances/$INSTANCE_ID/boot → 202"
    out=$(req POST "/v1/instances/$INSTANCE_ID/boot")
    assert_status 202 "$out"

    sub "DELETE /v1/instances/$INSTANCE_ID → 202 (status=deleting)"
    out=$(req DELETE "/v1/instances/$INSTANCE_ID")
    assert_status 202 "$out"
    body=$(body_of "$out")
    if [[ "$(field "$body" status)" == "deleting" ]]; then ok "status=deleting"; else ko "status=$(field "$body" status)"; fi
  fi
fi

############################################################################
# 9. 限流（默认不跑：要打满需 100+ rps；运维需要时手动开）
############################################################################
sub "限流（INCUSADMIN_E2E_RATELIMIT=1 时启动；默认跳过）"
if [[ "${INCUSADMIN_E2E_RATELIMIT:-0}" == "1" ]]; then
  hit429=0
  for i in $(seq 1 150); do
    status=$(curl -sS -o /dev/null -w '%{http_code}' "${H_AUTH[@]}" "${INCUSADMIN_BASE}/v1/account")
    if [[ "$status" == "429" ]]; then hit429=1; break; fi
  done
  if [[ $hit429 -eq 1 ]]; then
    ok "命中 429"
    retry=$(curl -sS -D - -o /dev/null "${H_AUTH[@]}" "${INCUSADMIN_BASE}/v1/account" | tr -d '\r' | grep -i 'retry-after\|ratelimit-' || true)
    [[ -n "$retry" ]] && ok "限流头：$retry" || ko "未带 RateLimit-* / Retry-After 头"
  else
    ko "150 次请求未命中 429（限流配置过宽？）"
  fi
else
  log "跳过（export INCUSADMIN_E2E_RATELIMIT=1 启用）"
fi

############################################################################
# 总结
############################################################################
printf '\n\033[1m=== 总结 ===\033[0m\n'
printf '  通过：%d\n  失败：%d\n' "$pass" "$fail"
if [[ $fail -gt 0 ]]; then exit 1; fi
exit 0
