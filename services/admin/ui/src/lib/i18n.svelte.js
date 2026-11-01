// @ts-check
import { zh } from './i18n.zh.js';
import { en } from './i18n.en.js';

const dicts = { zh, en };

let lang = $state(
  typeof localStorage !== 'undefined' ? localStorage.getItem('lang') || 'zh' : 'zh',
);

// 同步 <html lang>：屏幕阅读器发音 / 字体选择 / 翻译工具都依赖它。
// $effect.root 让模块级 $state 变化也被追踪，唯一的 lang 同步点收拢在此处。
if (typeof document !== 'undefined') {
  $effect.root(() => {
    $effect(() => {
      document.documentElement.lang = lang;
    });
  });
}

/**
 * @param {string} key
 * @param {Record<string, unknown>} [vars]
 */
export function t(key, vars) {
  const dictsAny = /** @type {Record<string, Record<string, string>>} */ (
    /** @type {unknown} */ (dicts)
  );
  let s = dictsAny[lang]?.[key] || dictsAny.en[key] || key;
  if (vars) {
    // 不在此处转义：t() 返回值用于普通文本插值 {t(...)}，Svelte 会自动转义。
    // 此处再转义会造成双重转义（如 "AT&T" 显示为 "AT&amp;T"）。
    for (const [k, v] of Object.entries(vars)) {
      s = s.split(`{${k}}`).join(v === null || v === undefined ? '' : String(v));
    }
  }
  return s;
}

export function toggleLang() {
  lang = lang === 'zh' ? 'en' : 'zh';
  if (typeof localStorage !== 'undefined') localStorage.setItem('lang', lang);
}

export function getLang() {
  return lang;
}
