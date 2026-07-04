#!/usr/bin/env node
/**
 * i18n 完整性校验：确保「代码中 t() 使用的静态 key」⊆「语言包 key 集合」，
 * 并确保 zh/en 两个语言包的 key 集合一一对齐。
 *
 * 规则：
 *  1. 仅校验可静态确定的 key —— `t("...")` / `t('...')` 形式的纯字符串字面量。
 *     动态 key（模板字符串 `t(`...${x}`)`、变量 `t(k)`）无法静态求值，跳过并计入统计。
 *  2. 静态 key 必须同时存在于 zh 与 en 语言包，否则视为「缺失 key」，脚本以非 0 退出。
 *  3. zh 与 en 的扁平 key 集合必须一致，任一方多出的 key 均报告并以非 0 退出。
 *
 * 用法：node scripts/check-i18n.mjs
 */
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(__dirname, "..");
const srcDir = join(webRoot, "src");
const localesDir = join(webRoot, "public", "locales");

/** 递归收集指定后缀的文件。 */
function walk(dir, exts) {
  const out = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      out.push(...walk(full, exts));
    } else if (exts.includes(extname(full))) {
      out.push(full);
    }
  }
  return out;
}

/** 把嵌套 JSON 扁平化成点分 key 集合。 */
function flatten(obj, prefix, set) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object" && !Array.isArray(v)) {
      flatten(v, key, set);
    } else {
      set.add(key);
    }
  }
}

/**
 * 从源码中提取静态 t() key。
 * 匹配 `t("...")` 或 `t('...')`（含 word boundary，排除 post()/get() 等以 t 结尾的函数）。
 * 模板字符串与变量入参不匹配 —— 计入 dynamic 统计。
 */
const STATIC_RE = /\bt\(\s*(['"])((?:\\.|(?!\1).)*)\1/g;
const DYNAMIC_RE = /\bt\(\s*(`|[A-Za-z_$])/g;

function extractKeys(files) {
  const staticKeys = new Map(); // key -> [locations]
  let dynamicCount = 0;
  for (const file of files) {
    const text = readFileSync(file, "utf8");
    const rel = file.slice(webRoot.length + 1);
    let m;
    STATIC_RE.lastIndex = 0;
    while ((m = STATIC_RE.exec(text))) {
      const key = m[2];
      // 过滤明显不是 i18n key 的入参（HTTP 路径以 / 开头等）
      if (key.startsWith("/") || key.startsWith("http")) continue;
      const line = text.slice(0, m.index).split("\n").length;
      if (!staticKeys.has(key)) staticKeys.set(key, []);
      staticKeys.get(key).push(`${rel}:${line}`);
    }
    DYNAMIC_RE.lastIndex = 0;
    while ((m = DYNAMIC_RE.exec(text))) {
      // 反引号 / 标识符入参 => 动态 key
      const ch = m[1];
      if (ch === "`") dynamicCount++;
      else {
        // 排除被 STATIC_RE 已覆盖的字符串场景（'/"），这里只统计标识符
        dynamicCount++;
      }
    }
  }
  return { staticKeys, dynamicCount };
}

function loadLocale(lng) {
  const file = join(localesDir, lng, "common.json");
  const set = new Set();
  flatten(JSON.parse(readFileSync(file, "utf8")), "", set);
  return set;
}

function main() {
  const files = walk(srcDir, [".ts", ".tsx"]);
  const { staticKeys, dynamicCount } = extractKeys(files);
  const zh = loadLocale("zh");
  const en = loadLocale("en");

  let failed = false;

  // 1. 静态 key ⊆ 语言包
  const missingZh = [];
  const missingEn = [];
  for (const key of staticKeys.keys()) {
    if (!zh.has(key)) missingZh.push(key);
    if (!en.has(key)) missingEn.push(key);
  }
  if (missingZh.length || missingEn.length) {
    failed = true;
    const all = [...new Set([...missingZh, ...missingEn])].sort();
    console.error(`\n✗ 代码使用了 ${all.length} 个语言包中缺失的 key：`);
    for (const key of all) {
      const where = [];
      if (!zh.has(key)) where.push("zh");
      if (!en.has(key)) where.push("en");
      console.error(`  - ${key}  (缺失于: ${where.join(",")})  用例: ${staticKeys.get(key)[0]}`);
    }
  }

  // 2. zh / en key 集合对齐
  const onlyZh = [...zh].filter((k) => !en.has(k)).sort();
  const onlyEn = [...en].filter((k) => !zh.has(k)).sort();
  if (onlyZh.length || onlyEn.length) {
    failed = true;
    if (onlyZh.length) {
      console.error(`\n✗ ${onlyZh.length} 个 key 仅存在于 zh，en 缺失：`);
      for (const k of onlyZh) console.error(`  - ${k}`);
    }
    if (onlyEn.length) {
      console.error(`\n✗ ${onlyEn.length} 个 key 仅存在于 en，zh 缺失：`);
      for (const k of onlyEn) console.error(`  - ${k}`);
    }
  }

  console.error(
    `\ni18n 校验：静态 key ${staticKeys.size} 个，动态 key ${dynamicCount} 处（跳过），` +
      `zh ${zh.size} 个，en ${en.size} 个。`,
  );

  if (failed) {
    console.error("\n✗ i18n 校验未通过。\n");
    process.exit(1);
  }
  console.error("✓ i18n 校验通过。\n");
}

main();
