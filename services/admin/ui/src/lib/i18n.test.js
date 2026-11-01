import { test, expect } from 'bun:test';
import { zh } from './i18n.zh.js';
import { en } from './i18n.en.js';

test('zh/en key sets match', () => {
  expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort());
});

test('zh/en placeholders match', () => {
  const placeholders = (s) => [...s.matchAll(/\{([a-zA-Z0-9_]+)\}/g)].map((m) => m[1]).sort();
  for (const key of Object.keys(zh)) {
    expect(placeholders(en[key])).toEqual(placeholders(zh[key]));
  }
});
