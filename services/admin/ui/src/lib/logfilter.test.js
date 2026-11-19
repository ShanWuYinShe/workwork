import { test, expect } from 'bun:test';
import { filterLogLines } from './logfilter.js';

const lines = [
  { id: 1, text: '2026/11/19 INFO server started', level: 'log' },
  { id: 2, text: '2026/11/19 ERROR xray dial failed', level: 'err' },
  { id: 3, text: '2026/11/19 INFO synced', level: 'log' },
];

test('空搜索返回原数组(同引用,与组件非过滤分支行为一致)', () => {
  expect(filterLogLines(lines, '')).toBe(lines);
});

test('大小写不敏感按 text 子串过滤', () => {
  expect(filterLogLines(lines, 'ERROR').map((l) => l.id)).toEqual([2]);
  expect(filterLogLines(lines, 'info').map((l) => l.id)).toEqual([1, 3]);
});

test('元素为对象时按 text 过滤(回归:旧实现对对象调 toLowerCase 即崩)', () => {
  const out = filterLogLines(lines, 'dial');
  expect(out.length).toBe(1);
  expect(out[0].id).toBe(2);
});
