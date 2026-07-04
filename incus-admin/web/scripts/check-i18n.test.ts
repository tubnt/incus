import { describe, expect, it } from "vitest";
// @ts-expect-error —— 纯 JS 校验脚本，无类型声明，测试直接引用其导出的纯函数。
import { extractStaticKeysFromSource, findMissingKeys, flatten } from "./check-i18n.mjs";

describe("check-i18n", () => {
  it("flatten 把嵌套 JSON 展平为点分 key 集合", () => {
    const set = new Set<string>();
    flatten({ a: { b: "x", c: "y" }, d: "z" }, "", set);
    expect([...set].sort()).toEqual(["a.b", "a.c", "d"]);
  });

  it("提取 t() 静态 key，排除 HTTP 路径与动态入参", () => {
    const src = `
      t("vm.title");
      t('common.delete');
      post(\`/portal/services/\${id}\`);
      t(\`ha.trigger\${x}\`);
      t(dynamicVar);
      t("http.should.skip.if.prefixed") // 不是 http 前缀键，正常纳入
    `;
    const keys = extractStaticKeysFromSource(src);
    expect(keys).toContain("vm.title");
    expect(keys).toContain("common.delete");
    // 模板字符串 / 变量入参不应被当作静态 key
    expect(keys).not.toContain("ha.trigger");
  });

  it("提取 mutation meta.successToast 的 key", () => {
    const src = `meta: { globalErrorToast: true, successToast: "vm.restoredToast" }`;
    expect(extractStaticKeysFromSource(src)).toContain("vm.restoredToast");
  });

  it("findMissingKeys 能检出语言包缺失的 key", () => {
    const codeKeys = ["a.b", "c.d", "e.f"];
    const locale = new Set(["a.b", "e.f"]);
    expect(findMissingKeys(codeKeys, locale)).toEqual(["c.d"]);
  });

  it("key 全部齐备时 findMissingKeys 返回空", () => {
    const codeKeys = ["a.b", "c.d"];
    const locale = new Set(["a.b", "c.d", "extra.z"]);
    expect(findMissingKeys(codeKeys, locale)).toEqual([]);
  });
});
