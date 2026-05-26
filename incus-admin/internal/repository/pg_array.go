package repository

import (
	"database/sql/driver"
	"fmt"
	"strings"
)

// PG TEXT[] 与 BIGINT[] 适配
//
// pgx/v5 stdlib 默认对 array 类型不自动 unmarshal；为了不引入 pgtype 依赖，
// 这里提供两组私有适配器：
//   - pgInt64Array / pgInt64Slice   ←→ BIGINT[]   （已用于 alert_rules.channel_ids）
//   - pgTextArray  / pgTextSlice    ←→ TEXT[]     （PLAN-054 products.period_supported）
//
// 数组容量都极小（period_supported ≤ 2，channel_ids 一般 < 10），string 操作够用。
// 注意：本实现假设元素不含 PostgreSQL array 字面量元字符（,{}\" 等）；
// period 取值固定为 'daily'/'monthly'，安全；外部如要复用，请先做转义评估。

type pgTextArray []string

// Value 实现 driver.Valuer。
//
// 输出 PostgreSQL array 字面量 `{a,b,c}`。nil → `{}`。
func (a pgTextArray) Value() (driver.Value, error) {
	if a == nil {
		return "{}", nil
	}
	// 严格断言：元素不含逗号/大括号/引号；若违反则返错而非默默写脏数据。
	for _, s := range a {
		if strings.ContainsAny(s, `,{}"\`) {
			return nil, fmt.Errorf("pgTextArray: element %q contains forbidden chars", s)
		}
	}
	return "{" + strings.Join(a, ",") + "}", nil
}

type pgTextSlice []string

// Scan 实现 sql.Scanner。
//
// 解析 PG 输出 `{a,b}` / 空 `{}` / NULL。元素仍假设不含逗号/引号。
func (s *pgTextSlice) Scan(src any) error {
	if src == nil {
		*s = nil
		return nil
	}
	var raw string
	switch v := src.(type) {
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		return fmt.Errorf("pgTextSlice: unexpected type %T", src)
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "{")
	raw = strings.TrimSuffix(raw, "}")
	if raw == "" {
		*s = []string{}
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		// PG TEXT[] 元素若含特殊字符会被双引号包裹；常规取值（'daily'/'monthly'）
		// 不会触发；按需 trim 引号以兼容显式带引号的输入。
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		out = append(out, p)
	}
	*s = out
	return nil
}
